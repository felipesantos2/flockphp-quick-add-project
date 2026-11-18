using Flock.Kit;
using Flock.Tray.Infrastructure;
namespace Flock.Tray.Features.Services;

/// <summary>Owns catalog reads and independent mutation lanes for each catalog service.</summary>
public sealed class CatalogService : IAsyncDisposable
{
    private const string CheckKey = "catalog:check";
    private readonly BatchGuard batchGuard;
    private readonly FlockClient client;
    private readonly SharedRead<ServiceCatalog> request;
    private readonly Dictionary<string, OperationErrors> resourceErrors = [];
    private readonly OperationRunner runner;
    private readonly TimeProvider timeProvider;

    public CatalogService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null, TimeProvider? timeProvider = null)
    {
        this.client = client;
        this.runner = runner;
        this.timeProvider = timeProvider ?? TimeProvider.System;
        request = new SharedRead<ServiceCatalog>(dispatcher);
        batchGuard = new BatchGuard(() => Changed?.Invoke());
        runner.RunningChanged += RunnerChanged;
    }

    public ServiceCatalog? Current { get; private set; }
    public bool HasSnapshot { get; private set; }
    public string? RefreshError => ErrorsFor("catalog").Refresh;
    public string? ActionError => resourceErrors.Values.Select(error => error.Action).FirstOrDefault(error => error is not null);
    public string? Error => ActionError ?? RefreshError;
    public bool IsChecking => runner.IsRunning(CheckKey);
    public Func<Task>? OnChange { get; set; }

    public async ValueTask DisposeAsync()
    {
        runner.RunningChanged -= RunnerChanged;
        await request.DisposeAsync();
    }
    public bool IsRunning(string id)
    {
        return runner.IsRunning(Key(id)) || batchGuard.IsHeld(id);
    }
    public bool CanCancelJob(string id)
    {
        return !runner.IsRunning(Key(id));
    }
    public event Action? Changed;

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default)
    {
        return request.JoinReadAsync(client.CatalogAsync, Apply, error => ErrorsFor("catalog").SetRefresh(error), observerToken, lifetimeToken: lifetimeToken);
    }

    public async Task<OperationOutcome> InstallAsync(string id, string? line = null, int? port = null, CancellationToken token = default)
    {
        if (!batchGuard.TryReserveMutation(id, out var reservation))
        {
            return OperationOutcome.Rejected;
        }

        using (reservation)
        {
            return await MutateAsync(id, cancellation => client.InstallCatalogServiceAsync(id, line, port, cancellation), token);
        }
    }

    public async Task<OperationOutcome> InstallUpdatesAsync(string id, IReadOnlyList<string> orderedLines, CancellationToken token = default)
    {
        if (!batchGuard.TryEnter(id, out var batch))
        {
            return OperationOutcome.Rejected;
        }

        using (batch)
        {
            try
            {
                foreach (var line in orderedLines)
                {
                    if (batch.WasCanceled)
                    {
                        return OperationOutcome.Succeeded;
                    }

                    var entry = await ReadCurrentEntryAsync(id, token);
                    var target = entry.Lines.SingleOrDefault(candidate => candidate.Id == line);
                    if (target is not { Installed: true })
                    {
                        throw new InvalidOperationException(Localization.Format(Strings.Services_Catalog_NoLongerInstalled, $"{entry.Name} {line}"));
                    }
                    if (!target.Update)
                    {
                        continue;
                    }

                    var outcome = await InstallForBatchAsync(id, line, token);
                    if (outcome == OperationOutcome.Rejected)
                    {
                        throw new InvalidOperationException(Localization.Format(Strings.Services_Catalog_UpdateRejected, $"{entry.Name} {line}"));
                    }
                    if (outcome != OperationOutcome.Succeeded)
                    {
                        return outcome;
                    }

                    entry = await PollForTerminalJobAsync(id, line, token);
                    if (entry.Job?.IsFailed == true || batch.WasCanceled)
                    {
                        return OperationOutcome.Succeeded;
                    }
                    if (entry.Lines.SingleOrDefault(candidate => candidate.Id == line)?.Update == true)
                    {
                        throw new InvalidOperationException(Localization.Format(Strings.Services_Catalog_UpdateUnfinished, $"{entry.Name} {line}"));
                    }
                }
                return OperationOutcome.Succeeded;
            }
            catch (OperationCanceledException) when (token.IsCancellationRequested)
            {
                return OperationOutcome.Canceled;
            }
            catch (Exception error)
            {
                ErrorsFor(id).SetAction(error);
                return OperationOutcome.Failed;
            }
        }
    }

    public Task<OperationOutcome> CancelAsync(string id, CancellationToken token = default)
    {
        return MutateAsync(id, cancellation => client.CancelCatalogJobAsync(id, cancellation), token, () => batchGuard.MarkCanceled(id));
    }

    public Task<OperationOutcome> DismissAsync(string id, CancellationToken token = default)
    {
        return MutateAsync(id, cancellation => client.DismissCatalogJobAsync(id, cancellation), token);
    }

    public Task<OperationOutcome> ActivateAsync(string id, string line, CancellationToken token = default)
    {
        return MutateAsync(id, cancellation => client.ActivateCatalogServiceAsync(id, line, cancellation), token);
    }

    public Task<OperationOutcome> UninstallAsync(string id, string? line, bool deleteData, CancellationToken token = default)
    {
        return MutateAsync(id, cancellation => client.UninstallCatalogServiceAsync(id, line, deleteData, cancellation), token);
    }

    public Task<OperationOutcome> CheckAsync(CancellationToken token = default)
    {
        return MutateAsync(CheckKey, cancellation => client.CheckCatalogAsync(cancellation), token);
    }

    public Task<string> ReadLogAsync(string id, CancellationToken token = default)
    {
        return client.CatalogLogAsync(id, token);
    }

    public void DismissError()
    {
        foreach (var error in resourceErrors.Values)
        {
            error.ClearAction();
            error.ClearRefresh();
        }
    }

    private async Task<OperationOutcome> MutateAsync(string id, Func<CancellationToken, Task<ServiceCatalog>> mutation, CancellationToken applicationToken,
        Action? afterSuccess = null)
    {
        var lane = id == CheckKey ? CheckKey : Key(id);
        var outcome = await runner.RunMutationAsync(lane, ErrorsFor(id), async token =>
        {
            request.Invalidate();
            try
            {
                var snapshot = await mutation(token);
                Apply(snapshot);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        if (outcome == OperationOutcome.Succeeded)
        {
            afterSuccess?.Invoke();
        }
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await request.RunReadAsync(client.CatalogAsync, Apply, error => ErrorsFor("catalog").SetRefresh(error), applicationToken);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    private Task<OperationOutcome> InstallForBatchAsync(string id, string line, CancellationToken token)
    {
        return MutateAsync(id, cancellation => client.InstallCatalogServiceAsync(id, line, cancellationToken: cancellation), token);
    }

    private async Task<Kit.CatalogService> ReadCurrentEntryAsync(string id, CancellationToken token)
    {
        request.Invalidate();
        try
        {
            var snapshot = await client.CatalogAsync(token);
            Apply(snapshot);
            var entry = snapshot[id] ?? throw new InvalidOperationException(Localization.Format(Strings.Services_Catalog_ServiceGone, id));
            if (!entry.Available)
            {
                throw new InvalidOperationException(Localization.Format(Strings.Services_Catalog_Unavailable, entry.Name));
            }
            return entry;
        }
        finally
        {
            request.Invalidate();
        }
    }

    private async Task<Kit.CatalogService> PollForTerminalJobAsync(string id, string line, CancellationToken token)
    {
        while (true)
        {
            await Task.Delay(TimeSpan.FromSeconds(2), timeProvider, token);
            var entry = await ReadCurrentEntryAsync(id, token);
            var target = entry.Lines.SingleOrDefault(candidate => candidate.Id == line);
            if (target is not { Installed: true })
            {
                throw new InvalidOperationException(Localization.Format(Strings.Services_Catalog_NoLongerInstalled, $"{entry.Name} {line}"));
            }
            if (entry.Job is null || entry.Job.IsFailed)
            {
                return entry;
            }
        }
    }

    private void Apply(ServiceCatalog value)
    {
        if (Current is null || !Same(Current, value))
        {
            Current = value;
            Changed?.Invoke();
        }
        ErrorsFor("catalog").ClearRefresh();
        if (!HasSnapshot)
        {
            HasSnapshot = true;
            Changed?.Invoke();
        }
    }

    private OperationErrors ErrorsFor(string id)
    {
        if (resourceErrors.TryGetValue(id, out var error))
        {
            return error;
        }
        error = new OperationErrors();
        error.PropertyChanged += (_, args) => { if (OperationErrors.IsDirectPropertyChange(args)) Changed?.Invoke(); };
        resourceErrors.Add(id, error);
        return error;
    }

    private void RunnerChanged(string key, bool running)
    {
        if (key == CheckKey || key.StartsWith("catalog:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private static string Key(string id)
    {
        return "catalog:" + id;
    }

    private static bool Same(ServiceCatalog left, ServiceCatalog right)
    {
        return left.CheckedAt == right.CheckedAt && left.CheckError == right.CheckError && left.Services.Count == right.Services.Count &&
               left.Services.Zip(right.Services).All(pair =>
                   pair.First.Id == pair.Second.Id && pair.First.Name == pair.Second.Name && pair.First.Category == pair.Second.Category &&
                   pair.First.Description == pair.Second.Description && pair.First.Available == pair.Second.Available &&
                   pair.First.Unavailable == pair.Second.Unavailable &&
                   pair.First.Estimate == pair.Second.Estimate && pair.First.Installed == pair.Second.Installed && pair.First.Line == pair.Second.Line &&
                   pair.First.Latest == pair.Second.Latest && pair.First.Update == pair.Second.Update && pair.First.Yanked == pair.Second.Yanked &&
                   pair.First.Port == pair.Second.Port && pair.First.DefaultPort == pair.Second.DefaultPort &&
                   pair.First.Lines.Count == pair.Second.Lines.Count && pair.First.Lines.Zip(pair.Second.Lines).All(lines =>
                       lines.First.Id == lines.Second.Id && lines.First.Label == lines.Second.Label && lines.First.Latest == lines.Second.Latest &&
                       lines.First.Installed == lines.Second.Installed && lines.First.Version == lines.Second.Version && lines.First.Active == lines.Second.Active &&
                       lines.First.Update == lines.Second.Update && lines.First.Yanked == lines.Second.Yanked && lines.First.HasData == lines.Second.HasData) &&
                   SameJob(pair.First.Job, pair.Second.Job));
    }

    private static bool SameJob(CatalogJob? left, CatalogJob? right)
    {
        return (left, right) switch
        {
            (null, null) => true,
            (null, _) or (_, null) => false,
            ({ } a, { } b) => a.Action == b.Action && a.State == b.State && a.Phase == b.Phase && a.Progress == b.Progress &&
                              a.Error == b.Error && a.StartedAt == b.StartedAt && a.Version == b.Version && a.Line == b.Line
        };
    }

    private sealed class BatchGuard(Action changed)
    {
        private readonly Dictionary<string, Batch> active = [];
        private readonly object gate = new();
        private readonly HashSet<string> mutationReservations = [];

        public bool IsHeld(string id)
        {
            lock (gate)
            {
                return active.ContainsKey(id);
            }
        }

        public bool TryEnter(string id, out Lease lease)
        {
            lock (gate)
            {
                if (active.ContainsKey(id) || mutationReservations.Contains(id))
                {
                    lease = null!;
                    return false;
                }
                var batch = new Batch();
                active.Add(id, batch);
                lease = new Lease(this, id, batch);
            }
            changed();
            return true;
        }

        public bool TryReserveMutation(string id, out IDisposable lease)
        {
            lock (gate)
            {
                if (active.ContainsKey(id) || !mutationReservations.Add(id))
                {
                    lease = null!;
                    return false;
                }
                lease = new MutationLease(this, id);
            }
            return true;
        }

        public void MarkCanceled(string id)
        {
            lock (gate)
            {
                if (!active.TryGetValue(id, out var batch))
                {
                    return;
                }
                batch.MarkCanceled();
            }
            changed();
        }

        private void Exit(string id, Batch batch)
        {
            var removed = false;
            lock (gate)
            {
                if (active.TryGetValue(id, out var current) && ReferenceEquals(current, batch))
                {
                    active.Remove(id);
                    removed = true;
                }
            }
            if (removed)
            {
                changed();
            }
        }

        private void ReleaseMutation(string id)
        {
            lock (gate)
            {
                mutationReservations.Remove(id);
            }
        }

        public sealed class Lease(BatchGuard owner, string id, Batch batch) : IDisposable
        {
            private int disposed;

            public Batch Batch { get; } = batch;
            public bool WasCanceled => Batch.WasCanceled;

            public void Dispose()
            {
                if (Interlocked.Exchange(ref disposed, 1) == 0)
                {
                    owner.Exit(id, Batch);
                }
            }
        }

        private sealed class MutationLease(BatchGuard owner, string id) : IDisposable
        {
            private int disposed;

            public void Dispose()
            {
                if (Interlocked.Exchange(ref disposed, 1) == 0)
                {
                    owner.ReleaseMutation(id);
                }
            }
        }

        public sealed class Batch
        {
            private int canceled;

            public bool WasCanceled => Volatile.Read(ref canceled) != 0;

            public void MarkCanceled()
            {
                Interlocked.Exchange(ref canceled, 1);
            }
        }
    }
}

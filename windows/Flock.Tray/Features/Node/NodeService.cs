using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Node;

/// <summary>Owns the Node catalog and one mutation lane per major.</summary>
public sealed class NodeService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly ConfigurationService configuration;
    private readonly SharedRead<NodeCatalog> request;
    private readonly Dictionary<string, OperationErrors> resourceErrors = [];
    private readonly Dictionary<string, string> runningActions = [];
    private readonly OperationErrors catalogErrors = new();
    private NodeCatalog? current;

    public NodeService(FlockClient client, OperationRunner runner, ConfigurationService configuration, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        this.configuration = configuration;
        request = new SharedRead<NodeCatalog>(dispatcher);
        catalogErrors.PropertyChanged += (_, args) => { if (OperationErrors.IsDirectPropertyChange(args)) Changed?.Invoke(); };
        runner.RunningChanged += RunnerChanged;
    }
    public NodeCatalog? Current => current;
    public string? RefreshError => catalogErrors.Refresh;
    public string? ActionError => resourceErrors.Values.Select(error => error.Action).FirstOrDefault(error => error is not null) ?? catalogErrors.Action;
    public string? Error => ActionError ?? RefreshError;
    public event Action? Changed;
    public Func<Task>? OnChange { get; set; }

    public bool IsRunning(string major) => runner.IsRunning(Key(major));
    public string? RunningAction(string major) => runningActions.GetValueOrDefault(major);
    public bool IsChecking => runner.IsRunning("node:catalog");

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        request.JoinReadAsync(client.NodeVersionsAsync, Apply, error => catalogErrors.SetRefresh(error), observerToken, lifetimeToken: lifetimeToken);

    public Task<OperationOutcome> InstallAsync(string major, CancellationToken token = default) =>
        MutateAsync(major, Strings.Node_DownloadingAndInstalling, cancellation => client.InstallNodeMajorAsync(major, cancellation), token);

    public Task<OperationOutcome> UpdateNpmAsync(string major, CancellationToken token = default) =>
        MutateAsync(major, Strings.Node_UpdatingNpm, cancellation => client.UpdateNpmAsync(major, cancellation), token);

    public Task<OperationOutcome> UninstallAsync(string major, CancellationToken token = default) =>
        MutateAsync(major, Strings.Node_Uninstalling, cancellation => client.UninstallNodeAsync(major, cancellation), token);

    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) =>
        MutateCatalogAsync(cancellation => client.CheckNodeReleasesAsync(cancellation), token);

    public async Task<OperationOutcome> SetDefaultAsync(string major, CancellationToken token = default)
    {
        var outcome = await configuration.SetDefaultNodeAsync(major, token);
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await RefreshAsync(token);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    public void DismissError()
    {
        catalogErrors.ClearAction();
        catalogErrors.ClearRefresh();
        foreach (var error in resourceErrors.Values)
        {
            error.ClearAction();
            error.ClearRefresh();
        }
    }

    public async ValueTask DisposeAsync()
    {
        runner.RunningChanged -= RunnerChanged;
        await request.DisposeAsync();
    }

    private async Task<OperationOutcome> MutateAsync(string major, string action, Func<CancellationToken, Task<NodeCatalog>> mutation, CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync(Key(major), ErrorsFor(major), async token =>
        {
            runningActions[major] = action;
            Changed?.Invoke();
            request.Invalidate();
            try
            {
                await mutation(token);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        await FollowUpAsync(outcome, applicationToken);
        return outcome;
    }

    private async Task<OperationOutcome> MutateCatalogAsync(Func<CancellationToken, Task<NodeCatalog>> mutation, CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync("node:catalog", catalogErrors, async token =>
        {
            request.Invalidate();
            try
            {
                await mutation(token);
            }
            finally
            {
                request.Invalidate();
            }
        }, applicationToken);
        await FollowUpAsync(outcome, applicationToken);
        return outcome;
    }

    private async Task FollowUpAsync(OperationOutcome outcome, CancellationToken token)
    {
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await RefreshAsync(token);
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
    }

    private void Apply(NodeCatalog value)
    {
        if (current is null || !Same(current, value))
        {
            current = value;
            Changed?.Invoke();
        }
        catalogErrors.ClearRefresh();
    }

    private OperationErrors ErrorsFor(string major)
    {
        if (resourceErrors.TryGetValue(major, out var error))
        {
            return error;
        }
        error = new OperationErrors();
        error.PropertyChanged += (_, args) => { if (OperationErrors.IsDirectPropertyChange(args)) Changed?.Invoke(); };
        resourceErrors.Add(major, error);
        return error;
    }

    private void RunnerChanged(string key, bool running)
    {
        if (key == "node:catalog" || key.StartsWith("node:", StringComparison.Ordinal))
        {
            if (!running && key != "node:catalog")
            {
                runningActions.Remove(key["node:".Length..]);
            }
            Changed?.Invoke();
        }
    }

    private static string Key(string major) => "node:" + major;

    private static bool Same(NodeCatalog left, NodeCatalog right) =>
        left.CheckedAt == right.CheckedAt && left.CheckError == right.CheckError && left.Versions.Count == right.Versions.Count &&
        left.Versions.Zip(right.Versions).All(pair => pair.First.Major == pair.Second.Major && pair.First.Latest == pair.Second.Latest && pair.First.Installed == pair.Second.Installed &&
            pair.First.IsDefault == pair.Second.IsDefault && pair.First.Update == pair.Second.Update && pair.First.Npm == pair.Second.Npm && pair.First.NpmLatest == pair.Second.NpmLatest);
}

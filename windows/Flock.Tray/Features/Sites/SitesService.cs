using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Sites;

/// <summary>Owns site mutations and extension reads; accepted status GETs remain the site snapshot owner.</summary>
public sealed class SitesService : IAsyncDisposable
{
    private readonly FlockClient client;
    private readonly OperationRunner runner;
    private readonly Dictionary<string, OperationErrors> errors = [];
    private readonly SharedRead<ExtensionReport> extensionRequest;
    private readonly object extensionGate = new();
    private string? extensionSite;
    private ExtensionReport? extensionReport;
    private string? extensionError;

    public SitesService(FlockClient client, OperationRunner runner, IUiDispatcher? dispatcher = null)
    {
        this.client = client;
        this.runner = runner;
        extensionRequest = new SharedRead<ExtensionReport>(dispatcher);
        runner.RunningChanged += RunningChanged;
    }

    /// <summary>Refreshes the shared status snapshot after a site action, including an uncertain failure.</summary>
    public Func<Task>? OnChange { get; set; }

    public bool IsRunning(string site) => runner.IsRunning(Key(site));

    public string? Error => errors.Values.Select(value => value.DisplayedMessage).FirstOrDefault(value => value is not null);
    public string? ActionError => errors.Values.Select(value => value.Action).FirstOrDefault(value => value is not null);

    public string? ErrorFor(string resource) => errors.TryGetValue(resource, out var value) ? value.DisplayedMessage : null;

    public event Action? Changed;

    public Task<OperationOutcome> LinkAsync(string path, CancellationToken token = default) =>
        MutateAsync("link:" + path, path, (_, cancellationToken) => client.LinkSiteAsync(path, cancellationToken: cancellationToken), token);

    public async Task<List<AppTemplate>> AppTemplatesAsync(CancellationToken token = default)
    {
        try
        {
            var templates = await client.AppTemplatesAsync(token);
            if (templates.Count > 0)
            {
                return templates;
            }
        }
        catch (FlockServerException error) when (error.StatusCode == 404)
        {
        }
        return
        [
            new AppTemplate { Name = "blank", RequiresDb = false },
            new AppTemplate { Name = "php", RequiresDb = false },
            new AppTemplate { Name = "wordpress", RequiresDb = true },
        ];
    }

    public Task<OperationOutcome> CreateAppAsync(string name, string parentDir, string type, bool db, string? dbService = null, bool secured = false, CancellationToken token = default) =>
        MutateAsync("create-app", name, (_, cancellationToken) => client.CreateAppAsync(name, parentDir, type, db, dbService, secured: secured, cancellationToken: cancellationToken), token);

    public Task<OperationOutcome> UnlinkAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.UnlinkSiteAsync(site, cancellationToken), token);

    public Task<OperationOutcome> UpdateAsync(string site, SiteChange change, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.UpdateSiteAsync(site, change, cancellationToken), token);

    public Task<OperationOutcome> RestartAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.RestartSiteAsync(site, cancellationToken), token);

    /// <summary>Adds a proxy, or changes its upstream (a port, host:port or URL). Site and proxy names never clash, so they share the site keys.</summary>
    public Task<OperationOutcome> ProxyAsync(string name, string upstream, CancellationToken token = default) =>
        MutateAsync(Key(name), name, (_, cancellationToken) => client.ProxyAsync(name, upstream, cancellationToken: cancellationToken), token);

    public Task<OperationOutcome> SecureProxyAsync(string name, bool secured, CancellationToken token = default) =>
        MutateAsync(Key(name), name, (_, cancellationToken) => client.SecureProxyAsync(name, secured, cancellationToken), token);

    public Task<OperationOutcome> RemoveProxyAsync(string name, CancellationToken token = default) =>
        MutateAsync(Key(name), name, (_, cancellationToken) => client.RemoveProxyAsync(name, cancellationToken), token);

    public Task<OperationOutcome> AddDomainAsync(string site, string domain, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.AddDomainAsync(site, domain, cancellationToken), token);

    public Task<OperationOutcome> RemoveDomainAsync(string site, string domain, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.RemoveDomainAsync(site, domain, cancellationToken), token);

    public Task<OperationOutcome> PinAsync(string site, string tool, string version, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.PinAsync(site, tool, version, cancellationToken), token);

    public Task<OperationOutcome> UnpinAsync(string site, string tool, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.UnpinAsync(site, tool, cancellationToken), token);

    public Task<OperationOutcome> AddBoostGuidelineAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.AddBoostGuidelineAsync(site, cancellationToken), token);

    public Task<OperationOutcome> StartShareAsync(string site, IReadOnlyList<string> allow, bool debug, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.StartShareAsync(site, allow, debug, cancellationToken), token);

    public Task<OperationOutcome> RebuildShareAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.RebuildShareAsync(site, cancellationToken), token);

    public Task<OperationOutcome> StopShareAsync(string site, CancellationToken token = default) =>
        MutateAsync(Key(site), site, (_, cancellationToken) => client.StopShareAsync(site, cancellationToken), token);

    /// <summary>A site's git branches for the New Worktree dialog; null when the daemon can't list them (an older one, not a git checkout).</summary>
    public async Task<SiteBranches?> SiteBranchesAsync(string site, CancellationToken token = default)
    {
        try
        {
            return await client.SiteBranchesAsync(site, token);
        }
        catch (FlockException)
        {
            return null;
        }
    }

    /// <summary>
    /// Creates a git worktree of site on branch (empty: Flock makes a name up), from from (null: the current commit), with the
    /// database mode db (null: the project's). The dialog asking for it shows a failure, so it
    /// returns the error instead of putting it into the window's error bar.
    /// </summary>
    public async Task<(string? Path, string? Error)> CreateWorktreeAsync(string site, string branch, string? from, string? db, CancellationToken token = default)
    {
        string? path = null;
        var error = await RunInDialogAsync(Key(site), async cancellationToken =>
            path = (await client.CreateWorktreeAsync(site, branch, from, db: db, cancellationToken: cancellationToken)).Path, token);
        return (path, error);
    }

    /// <summary>Sets the worktree (a site with a parent) up, or once more (again); its progress dialog shows a failure.</summary>
    public Task<string?> SetupWorktreeAsync(string worktree, string path, bool again, CancellationToken token = default) =>
        RunInDialogAsync(Key(worktree), cancellationToken => client.SetupWorktreeAsync(path, again, cancellationToken: cancellationToken), token);

    /// <summary>Gives the worktree a database by mode (the one Flock made for it before is dropped); its progress dialog shows a failure.</summary>
    public Task<string?> SwitchWorktreeDatabaseAsync(string worktree, string path, string db, CancellationToken token = default) =>
        RunInDialogAsync(Key(worktree), cancellationToken => client.SwitchWorktreeDatabaseAsync(path, db, cancellationToken), token);

    /// <summary>
    /// Removes the worktree at path, and the database Flock made for it. Returns the error for the
    /// dialog to offer removing it anyway (force).
    /// </summary>
    public Task<string?> RemoveWorktreeAsync(string worktree, string path, bool force, CancellationToken token = default) =>
        RunInDialogAsync(Key(worktree), cancellationToken => client.RemoveWorktreeAsync(path, force, cancellationToken), token);

    /// <summary>Reads one selected site's extensions. A new site retires the prior read; equivalent reads join.</summary>
    public async Task<(ExtensionReport? Report, string? Error)> SiteExtensionsAsync(string site, CancellationToken token = default)
    {
        Task read;
        lock (extensionGate)
        {
            if (extensionSite == site)
            {
                read = extensionRequest.JoinReadAsync(clientToken => client.SiteExtensionsAsync(site, clientToken),
                    value => PublishExtension(site, value, null), error => PublishExtension(site, null, error.Message), token);
            }
            else
            {
                extensionSite = site;
                extensionReport = null;
                extensionError = null;
                extensionRequest.Invalidate();
                read = extensionRequest.RunReadAsync(clientToken => client.SiteExtensionsAsync(site, clientToken),
                    value => PublishExtension(site, value, null), error => PublishExtension(site, null, error.Message), token);
            }
        }
        await read;
        lock (extensionGate)
        {
            return extensionSite == site ? (extensionReport, extensionError) : (null, null);
        }
    }

    /// <summary>Requires an extension, then reloads its report through the same selected-report owner.</summary>
    public async Task<(ExtensionReport? Report, string? Error)> RequireExtensionAsync(string site, string name, CancellationToken token = default)
    {
        var outcome = await MutateAsync(Key(site), site, async (_, cancellationToken) =>
        {
            extensionRequest.Invalidate();
            await client.RequireExtensionAsync(site, name, cancellationToken);
        }, token);
        if (outcome is OperationOutcome.Rejected or OperationOutcome.Canceled)
        {
            return (null, outcome == OperationOutcome.Canceled ? "Extension request canceled." : null);
        }
        return await SiteExtensionsAsync(site, token);
    }

    public void DismissError(string resource)
    {
        if (errors.TryGetValue(resource, out var value))
        {
            value.ClearAction();
            value.ClearRefresh();
        }
    }

    public void DismissError()
    {
        foreach (var value in errors.Values)
        {
            value.ClearAction();
            value.ClearRefresh();
        }
    }

    public async ValueTask DisposeAsync()
    {
        runner.RunningChanged -= RunningChanged;
        await extensionRequest.DisposeAsync();
    }

    private async Task<OperationOutcome> MutateAsync(string key, string resource, Func<FlockClient, CancellationToken, Task> mutation,
        CancellationToken applicationToken)
    {
        var outcome = await runner.RunMutationAsync(key, ErrorsFor(resource), token => mutation(client, token), applicationToken);
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            if (OnChange is not null)
            {
                await OnChange();
            }
        }
        return outcome;
    }

    /// <summary>A mutation whose failure its dialog shows: the error message, or null when it succeeded.</summary>
    private async Task<string?> RunInDialogAsync(string key, Func<CancellationToken, Task> mutation, CancellationToken token)
    {
        var errors = new OperationErrors();
        var outcome = await runner.RunMutationAsync(key, errors, mutation, token);
        if ((outcome is OperationOutcome.Succeeded or OperationOutcome.Failed) && OnChange is not null)
        {
            await OnChange();
        }
        return outcome switch
        {
            OperationOutcome.Rejected => "Another change to this site is still running. Try again when it's done.",
            OperationOutcome.Canceled => "Canceled.",
            _ => errors.Action,
        };
    }

    private OperationErrors ErrorsFor(string resource)
    {
        if (errors.TryGetValue(resource, out var value))
        {
            return value;
        }
        value = new OperationErrors();
        value.PropertyChanged += (_, args) => { if (OperationErrors.IsDirectPropertyChange(args)) Changed?.Invoke(); };
        errors.Add(resource, value);
        return value;
    }

    private void RunningChanged(string key, bool running)
    {
        if (key.StartsWith("site:", StringComparison.Ordinal) || key.StartsWith("link:", StringComparison.Ordinal))
        {
            Changed?.Invoke();
        }
    }

    private void PublishExtension(string site, ExtensionReport? report, string? error)
    {
        lock (extensionGate)
        {
            if (extensionSite == site)
            {
                extensionReport = report;
                extensionError = error;
            }
        }
    }

    private static string Key(string site) => "site:" + site;
}

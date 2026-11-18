using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;

namespace Flock.Tray.ViewModels;

/// <summary>Retained panel state and commands projected from the application model.</summary>
public sealed class PanelViewModel : ObservableObject, IDisposable
{
    private readonly RuntimeService model;
    private readonly CollectionSynchronizer<string, PanelSiteRow> siteSynchronizer = new();
    private readonly CollectionSynchronizer<string, PanelServiceRow> serviceSynchronizer = new();
    private readonly CollectionSynchronizer<string, PanelProxyRow> proxySynchronizer = new();
    private readonly DaemonSwitch xdebug;
    private readonly DaemonSwitch dumps;
    private HashSet<string>? expanded;
    private bool released;

    public PanelViewModel(RuntimeService model)
    {
        this.model = model;
        xdebug = new DaemonSwitch(() => OnPropertyChanged(nameof(Xdebug)));
        dumps = new DaemonSwitch(() => OnPropertyChanged(nameof(Dumps)));
        model.Changed += ModelChanged;
        model.UpdateChanged += UpdateChanged;
        model.Dumps.Changed += DumpsChanged;
        ToggleDumpsCommand = new AsyncRelayCommand<bool>(SetDumpsAsync, _ => !model.Dumps.IsCaptureBusy);
        ToggleXdebugCommand = new AsyncRelayCommand<bool>(SetXdebugAsync);
        RestartCommand = new AsyncRelayCommand(model.RestartAsync, () => !IsRestarting);
        InstallUpdateCommand = new RelayCommand(() => App.Current.InstallUpdate(), () => CanInstallUpdate);
        OpenMailCommand = new RelayCommand(() => App.Current.Open(WindowKind.Mail));
        OpenDumpsCommand = new RelayCommand(() => App.Current.Open(WindowKind.Dumps));
        OpenLogsCommand = new RelayCommand(() => App.Current.Open(WindowKind.Logs));
        OpenSetupCommand = new RelayCommand(() => App.Current.Open(WindowKind.Setup));
        OpenSettingsCommand = new RelayCommand(() => App.Current.Open(WindowKind.Settings));
        QuitCommand = new RelayCommand(() => App.Current.Quit());
        RefreshRows();
    }

    public RuntimeService Model => model;
    public Status? Status => model.Status;
    public string? Error => model.Error;

    /// <summary>The Xdebug switch, bound two-way: setting it asks the daemon, reading it follows the daemon.</summary>
    public bool Xdebug
    {
        get => xdebug.Show(Status?.Xdebug == true);
        set
        {
            if (value != Xdebug)
            {
                ToggleXdebugCommand.Execute(value);
            }
        }
    }

    /// <summary>The dump capture switch, bound two-way like <see cref="Xdebug" />.</summary>
    public bool Dumps
    {
        get => dumps.Show(Status?.Dumps == true);
        set
        {
            if (value != Dumps)
            {
                ToggleDumpsCommand.Execute(value);
            }
        }
    }
    public string? ActionError => model.RuntimeError ?? model.ActionError ?? model.Dumps.ActionError;
    public bool IsRunning => model.IsRunning;
    public bool IsDegraded => model.IsDegraded;
    public bool IsRestarting => model.IsRestarting;
    public bool IsUpdating => model.IsUpdating;
    public bool IsCaptureBusy => model.Dumps.IsCaptureBusy;
    public bool NeedsSetup => model.NeedsSetup;
    public string Headline => model.Headline;
    public string DisplayName => Application.Current is App app ? app.Options.Identity.DisplayName : "Flock";
    public UpdateFeed.Item? AvailableUpdate => model.UpdateService.Available;
    public Features.Update.UpdatePhase UpdateState => model.UpdateService.State;
    public bool HasStatusContent => Status is not null && !NeedsSetup;
    public bool HasSites => HasStatusContent && (Sites.Count > 0 || Proxies.Count > 0);
    public string NotRunningMessage => model.Error ?? Strings.Common_FlockNotRunning;
    public bool HasActionError => ActionError is not null;
    public bool HasUpdate => AvailableUpdate is not null;
    public bool IsUpdateBusy => model.UpdateService.IsBusy;
    public bool CanInstallUpdate => HasUpdate && !IsUpdateBusy;
    public string UpdateMessage => !IsUpdateBusy && AvailableUpdate is { } update ? Localization.Format(Strings.Menu_UpdateAvailable, update.Version) : model.UpdateService.Summary;
    public int MailCount => Status?.MailCount ?? 0;
    public int DumpCount => Status?.DumpCount ?? 0;
    public Visibility RestartVisibility => IsRestarting ? Visibility.Visible : Visibility.Collapsed;
    public Visibility UpdateVisibility => HasUpdate ? Visibility.Visible : Visibility.Collapsed;
    public Visibility StatusContentVisibility => HasStatusContent ? Visibility.Visible : Visibility.Collapsed;
    public Visibility WarningsVisibility => Status?.Warnings.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    public Visibility SitesVisibility => HasSites ? Visibility.Visible : Visibility.Collapsed;
    public Visibility EmptySitesVisibility => HasSites ? Visibility.Collapsed : Visibility.Visible;
    public Visibility NotRunningVisibility => HasStatusContent ? Visibility.Collapsed : Visibility.Visible;
    public Visibility SetupVisibility => NeedsSetup ? Visibility.Visible : Visibility.Collapsed;
    public Visibility UpdatingVisibility => !NeedsSetup && IsUpdating ? Visibility.Visible : Visibility.Collapsed;
    public Visibility StoppedVisibility => !NeedsSetup && !IsUpdating ? Visibility.Visible : Visibility.Collapsed;
    public Visibility FooterVisibility => HasStatusContent ? Visibility.Visible : Visibility.Collapsed;
    public Visibility MailCountVisibility => MailCount > 0 ? Visibility.Visible : Visibility.Collapsed;
    public Visibility DumpCountVisibility => DumpCount > 0 ? Visibility.Visible : Visibility.Collapsed;
    public bool CaptureEnabled => !IsCaptureBusy;
    public bool StartEnabled => !IsRestarting;
    public bool RestartEnabled => IsRunning && !IsRestarting;
    public ObservableCollection<PanelSiteRow> Sites { get; } = [];
    public ObservableCollection<PanelServiceRow> Services { get; } = [];
    /// <summary>Proxies (`flock proxy`), listed after the sites.</summary>
    public ObservableCollection<PanelProxyRow> Proxies { get; } = [];
    public IAsyncRelayCommand<bool> ToggleDumpsCommand { get; }
    public IAsyncRelayCommand<bool> ToggleXdebugCommand { get; }
    public IAsyncRelayCommand RestartCommand { get; }
    public IRelayCommand InstallUpdateCommand { get; }
    public IRelayCommand OpenMailCommand { get; }
    public IRelayCommand OpenDumpsCommand { get; }
    public IRelayCommand OpenLogsCommand { get; }
    public IRelayCommand OpenSetupCommand { get; }
    public IRelayCommand OpenSettingsCommand { get; }
    public IRelayCommand QuitCommand { get; }

    public Task SetDumpsAsync(bool enabled) => dumps.ChangeAsync(enabled, model.SetDumpsAsync);

    public Task SetXdebugAsync(bool enabled) => xdebug.ChangeAsync(enabled, model.SetXdebugAsync);

    public void OpenSite(string url) => model.Open(url);

    /// <summary>Shows or hides a site's worktrees under it, and remembers it in the preferences.</summary>
    public void ToggleWorktrees(string site)
    {
        if (!Expanded.Add(site))
        {
            Expanded.Remove(site);
        }
        RefreshRows();
        _ = SaveExpandedAsync(string.Join('\n', Expanded.Order(StringComparer.Ordinal)));
    }

    /// <summary>Shows a site's worktrees without remembering it (for screenshots).</summary>
    internal void ShowWorktrees(string site)
    {
        if (Expanded.Add(site))
        {
            RefreshRows();
        }
    }

    // Read once: the panel's own toggles are the newer state from then on.
    private HashSet<string> Expanded => expanded ??= model.Preferences.Snapshot.ExpandedSites
        .Split('\n', StringSplitOptions.RemoveEmptyEntries).ToHashSet(StringComparer.Ordinal);

    private async Task SaveExpandedAsync(string value)
    {
        try
        {
            await model.Preferences.UpdateAsync(data => data with { ExpandedSites = value });
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Text.Json.JsonException or OperationCanceledException)
        {
            // PreferencesService reports a failed save (Error); the panel keeps the state until it quits.
        }
    }

    public void DismissError()
    {
        model.DismissError();
        model.Dumps.DismissError();
    }

    public void Dispose()
    {
        if (released)
        {
            return;
        }
        released = true;
        model.Changed -= ModelChanged;
        model.UpdateChanged -= UpdateChanged;
        model.Dumps.Changed -= DumpsChanged;
    }

    private void ModelChanged()
    {
        RefreshRows();
        OnPropertyChanged(nameof(Status));
        OnPropertyChanged(nameof(Xdebug));
        OnPropertyChanged(nameof(Dumps));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(IsRunning));
        OnPropertyChanged(nameof(IsDegraded));
        OnPropertyChanged(nameof(IsRestarting));
        OnPropertyChanged(nameof(IsUpdating));
        OnPropertyChanged(nameof(IsCaptureBusy));
        OnPropertyChanged(nameof(NeedsSetup));
        OnPropertyChanged(nameof(Headline));
        OnPropertyChanged(nameof(DisplayName));
        OnPropertyChanged(nameof(HasStatusContent));
        OnPropertyChanged(nameof(HasSites));
        OnPropertyChanged(nameof(NotRunningMessage));
        OnPropertyChanged(nameof(HasActionError));
        OnPropertyChanged(nameof(MailCount));
        OnPropertyChanged(nameof(DumpCount));
        OnPropertyChanged(nameof(RestartVisibility));
        OnPropertyChanged(nameof(StatusContentVisibility));
        OnPropertyChanged(nameof(WarningsVisibility));
        OnPropertyChanged(nameof(SitesVisibility));
        OnPropertyChanged(nameof(EmptySitesVisibility));
        OnPropertyChanged(nameof(NotRunningVisibility));
        OnPropertyChanged(nameof(SetupVisibility));
        OnPropertyChanged(nameof(UpdatingVisibility));
        OnPropertyChanged(nameof(StoppedVisibility));
        OnPropertyChanged(nameof(FooterVisibility));
        OnPropertyChanged(nameof(MailCountVisibility));
        OnPropertyChanged(nameof(DumpCountVisibility));
        OnPropertyChanged(nameof(CaptureEnabled));
        OnPropertyChanged(nameof(StartEnabled));
        OnPropertyChanged(nameof(RestartEnabled));
        RestartCommand.NotifyCanExecuteChanged();
        ToggleDumpsCommand.NotifyCanExecuteChanged();
    }

    // Update progress has its own event: on the shared one every download tick would redraw the whole panel.
    private void UpdateChanged()
    {
        OnPropertyChanged(nameof(AvailableUpdate));
        OnPropertyChanged(nameof(UpdateState));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(IsUpdateBusy));
        OnPropertyChanged(nameof(CanInstallUpdate));
        OnPropertyChanged(nameof(UpdateMessage));
        OnPropertyChanged(nameof(UpdateVisibility));
        InstallUpdateCommand.NotifyCanExecuteChanged();
    }

    private void DumpsChanged()
    {
        OnPropertyChanged(nameof(ActionError));
        OnPropertyChanged(nameof(IsCaptureBusy));
        OnPropertyChanged(nameof(HasActionError));
        OnPropertyChanged(nameof(UpdatingVisibility));
        OnPropertyChanged(nameof(StoppedVisibility));
        OnPropertyChanged(nameof(CaptureEnabled));
        ToggleDumpsCommand.NotifyCanExecuteChanged();
    }

    private void RefreshRows()
    {
        var status = model.Status;
        // Worktrees are listed under their site, which shows them when it is expanded.
        List<PanelSiteValue> sites = status is null ? [] : status.Sites.Where(site => !site.IsWorktree)
            .Select(site => new PanelSiteValue(site, status.WorktreesOf(site.Name), Expanded.Contains(site.Name))).ToList();
        siteSynchronizer.Synchronize(Sites, sites, row => row.Domain, value => value.Site.Domain,
            value => new PanelSiteRow(value), (row, value) => row.Update(value));
        List<Status.Proxy> proxies = status?.Proxies ?? [];
        proxySynchronizer.Synchronize(Proxies, proxies, row => row.Name, proxy => proxy.Name,
            proxy => new PanelProxyRow(proxy), (row, proxy) => row.Update(proxy));

        List<PanelServiceValue> values = status is null ? [] : BuildServices(status);
        serviceSynchronizer.Synchronize(Services, values, row => row.Id, row => row.Id,
            value => new PanelServiceRow(value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(HasSites));
    }

    private static List<PanelServiceValue> BuildServices(Status status)
    {
        var values = new List<PanelServiceValue>
        {
            new("web", Strings.Menu_Web, $"http :{status.HttpPort} · https :{status.HttpsPort}", true),
        };
        if (status.Dns.Wildcard)
        {
            values.Add(new("dns", "DNS", $"*.{status.Dns.Tld}", true));
        }
        else if (status.Dns.Tld.Length > 0)
        {
            var helperDown = status.Warnings.Any(w => w.Contains("hosts helper", StringComparison.Ordinal));
            values.Add(new("hosts", "Hosts", Strings.Menu_SiteDomainsOnly, !helperDown));
        }
        values.Add(new("mail", Strings.Menu_Mail, status.Mail.Length == 0 ? Strings.Menu_Stopped : $"smtp {status.Mail}", status.Mail.Length > 0));
        if (status.Postgres is { } postgres)
        {
            values.Add(new("postgres", "PostgreSQL", postgres, status.Processes.FirstOrDefault(p => p.Name == "postgres")?.Up ?? false));
        }
        foreach (var extra in status.Extras)
        {
            values.Add(new(extra.Name, extra.Name, extra.Enabled ? Localization.Format(Strings.Menu_VersionOnPort, extra.Version, extra.Port) : Strings.Menu_Stopped, extra.Up));
        }
        foreach (var pool in status.Processes.Where(p => p.Name.StartsWith("php", StringComparison.Ordinal)).GroupBy(p => PoolName(p.Name)))
        {
            var workers = pool.ToList();
            var up = workers.Count(worker => worker.Up);
            var detail = up == 0
                ? workers.FirstOrDefault(worker => worker.LastError is not null)?.LastError ?? Strings.Menu_Down
                : workers.Count > 1 ? Localization.Format(Strings.Menu_WorkersUp, up, workers.Count) : Strings.Menu_Up;
            var restarts = workers.Sum(worker => worker.Restarts);
            if (up > 0 && restarts > 0)
            {
                detail += " " + Localization.Format(Strings.Menu_Restarted, restarts);
            }
            values.Add(new PanelServiceValue(pool.Key, pool.Key, detail, up > 0));
        }
        foreach (var process in status.Processes.Where(p => !p.Up && !p.Name.StartsWith("php", StringComparison.Ordinal)))
        {
            values.Add(new PanelServiceValue("process:" + process.Name, process.Name, process.LastError ?? Strings.Menu_Down, false));
        }
        return values;
    }

    private static string PoolName(string process)
    {
        var match = System.Text.RegularExpressions.Regex.Match(process, @"^php(\d+\.\d+)-(?:cgi|fpm)");
        return match.Success ? $"PHP {match.Groups[1].Value}" : process;
    }
}

/// <summary>A site of the panel, with its worktrees and whether they are shown.</summary>
public sealed record PanelSiteValue(Status.Site Site, IReadOnlyList<Status.Site> Worktrees, bool Expanded);

public sealed class PanelSiteRow(PanelSiteValue value) : ObservableObject
{
    private Status.Site current = value.Site;
    private PanelSiteValue entry = value;

    public string Name => current.Name;
    public bool Expanded => entry.Expanded;
    public List<PanelWorktreeRow> Worktrees => Expanded ? entry.Worktrees.Select(worktree => new PanelWorktreeRow(worktree)).ToList() : [];
    public Visibility WorktreesVisibility => Expanded && entry.Worktrees.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    public Visibility ToggleVisibility => entry.Worktrees.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    public string WorktreeCount => entry.Worktrees.Count.ToString(System.Globalization.CultureInfo.CurrentCulture);
    public string ToggleGlyph => Expanded ? "\uE70D" : "\uE76C";
    public string ToggleLabel => Expanded ? Strings.Menu_HideWorktrees : Localization.Plural(Strings.ResourceManager, "Menu_ShowWorktrees", entry.Worktrees.Count);
    public string Domain => current.Domain;
    public string Path => current.Path;
    public string? Url => current.Url;
    public string? Problem => current.Problem;
    public bool Secured => current.Secured;
    public string Php => current.Php;
    public bool Debugging => current.Debugging;
    public bool IsFrankenPhp => current.IsFrankenPhp;
    public string Worker => current.Worker;
    public bool IsShared => current.IsShared;
    public string? ShareUrl => current.Share?.Url;
    public bool HasUrl => Url is not null;
    public string SiteTooltip => Url ?? Problem ?? Path;
    public string SecurityGlyph => Secured ? "\uE72E" : "\uE774";
    /// <summary>What runs the site: "PHP 8.4", or "Node.js 22" for a Node.js app.</summary>
    public string PhpLabel => current.IsNode ? $"Node.js {current.Node}" : $"PHP {Php}";
    public string WorkerLabel => Worker == Status.SiteServer.Octane ? "Octane" : "FrankenPHP";
    public string ShareTooltip => Localization.Format(Strings.Menu_SharedAt, ShareUrl);
    public Visibility ProblemVisibility => Problem is not null ? Visibility.Visible : Visibility.Collapsed;
    public Visibility DebuggingVisibility => Debugging ? Visibility.Visible : Visibility.Collapsed;
    public Visibility FrankenPhpVisibility => IsFrankenPhp ? Visibility.Visible : Visibility.Collapsed;
    public Visibility SharedVisibility => IsShared ? Visibility.Visible : Visibility.Collapsed;
    public Brush SecurityBrush => (Brush)Application.Current.Resources[Secured ? "SystemFillColorSuccessBrush" : "TextFillColorSecondaryBrush"];
    /// <summary>A Node.js site's dev server as a dot: sleeping, starting, running or failed.</summary>
    public Visibility DotVisibility => current.NodeServer is not null ? Visibility.Visible : Visibility.Collapsed;
    public string DotTooltip => current.NodeServer?.StateLabel ?? "";
    public Brush DotBrush => (Brush)Application.Current.Resources[current.NodeServer?.State switch
    {
        Status.NodeServerState.Running => "SystemFillColorSuccessBrush",
        Status.NodeServerState.Starting => "SystemFillColorCautionBrush",
        Status.NodeServerState.Failed => "SystemFillColorCriticalBrush",
        _ => "ControlStrongFillColorDisabledBrush",
    }];

    public void Update(PanelSiteValue value)
    {
        entry = value;
        current = value.Site;
        OnPropertyChanged(string.Empty);
    }
}

/// <summary>A proxy after the sites: its domain, and what answers at its upstream or that nothing does yet.</summary>
public sealed class PanelProxyRow(Status.Proxy proxy) : ObservableObject
{
    private Status.Proxy current = proxy;

    public string Name => current.Name;
    public string Domain => current.Domain;
    public string? Url => current.Url;
    public bool HasUrl => Url is not null;
    public string? Problem => current.Problem;
    public bool Running => current.Running;
    public string ServerLabel => current.ServerLabel;
    public string Tooltip => Running ? $"{Url ?? Domain} → {current.Upstream}"
        : current.Command is { } command ? Localization.Format(Strings.Sites_Proxy_StartInSettings, command.StateLabel)
        : Localization.Format(Strings.Sites_Proxy_NothingAnswers, current.Upstream);
    public string SecurityGlyph => current.Secured ? "\uE72E" : "\uE774";
    public Visibility ProblemVisibility => Problem is not null ? Visibility.Visible : Visibility.Collapsed;
    public Brush SecurityBrush => (Brush)Application.Current.Resources[current.Secured ? "SystemFillColorSuccessBrush" : "TextFillColorSecondaryBrush"];
    /// <summary>The command's state when Flock starts the server, else whether something answers.</summary>
    public Brush StatusBrush => (Brush)Application.Current.Resources[current.Command?.State switch
    {
        Status.ProxyState.Failed => "SystemFillColorCriticalBrush",
        Status.ProxyState.Starting or Status.ProxyState.Stopping => "SystemFillColorCautionBrush",
        _ => Running ? "SystemFillColorSuccessBrush" : "ControlStrongFillColorDisabledBrush",
    }];

    public void Update(Status.Proxy value)
    {
        current = value;
        OnPropertyChanged(string.Empty);
    }
}

/// <summary>A worktree, indented under its site: its branch, its own database and its setup.</summary>
public sealed class PanelWorktreeRow(Status.Site worktree)
{
    public string Label => worktree.WorktreeLabel;
    public string? Url => worktree.Url;
    public bool HasUrl => Url is not null;
    public string Tooltip => Url ?? worktree.Problem ?? worktree.Path;
    public string? Message => worktree.Problem ?? (worktree.Worktree is { IsFailed: true, Error.Length: > 0 } setup ? setup.Error : null);
    public string SecurityGlyph => worktree.Secured ? "\uE72E" : "\uE774";
    public string DatabaseLabel => worktree.Worktree is { DbMode.Length: > 0 } setup ? setup.DbMode : Strings.Worktrees_OwnDatabase;
    public string DatabaseTooltip => Localization.Format(Strings.Worktrees_OwnDatabaseTooltip, worktree.Worktree?.Database);
    public string Step => worktree.Worktree is { Step.Length: > 0 } setup ? setup.Step : Strings.Worktrees_SettingUp;
    public Visibility MessageVisibility => Message is not null ? Visibility.Visible : Visibility.Collapsed;
    public Visibility DatabaseVisibility => worktree.Worktree is { HasOwnDatabase: true } ? Visibility.Visible : Visibility.Collapsed;
    public Visibility StepVisibility => worktree.Worktree is { IsBusy: true } ? Visibility.Visible : Visibility.Collapsed;
    public Brush SecurityBrush => (Brush)Application.Current.Resources[worktree.Secured ? "SystemFillColorSuccessBrush" : "TextFillColorSecondaryBrush"];
}

public sealed class PanelServiceRow(PanelServiceValue value) : ObservableObject
{
    private PanelServiceValue current = value;

    public string Id => current.Id;
    public string Name => current.Name;
    public string Detail => current.Detail;
    public bool Up => current.Up;
    public Brush StatusBrush => (Brush)Application.Current.Resources[Up ? "SystemFillColorSuccessBrush" : "ControlStrongFillColorDisabledBrush"];

    public void Update(PanelServiceValue value)
    {
        current = value;
        OnPropertyChanged(string.Empty);
    }
}

public sealed record PanelServiceValue(string Id, string Name, string Detail, bool Up);

/// <summary>
///     Backs a switch bound two-way to daemon state. While a change runs the switch keeps the requested value;
///     once it ends the switch shows the daemon's state again, so a refused or failed change snaps back.
/// </summary>
public sealed class DaemonSwitch(Action notify)
{
    private bool? pending;
    private int generation;

    /// <summary>The value the switch shows: the requested one while a change runs, otherwise <paramref name="actual" />.</summary>
    public bool Show(bool actual) => pending ?? actual;

    public async Task ChangeAsync(bool value, Func<bool, Task> change)
    {
        var current = ++generation;
        pending = value;
        try
        {
            await change(value);
        }
        finally
        {
            // A change that ends at once would otherwise notify while the binding is still writing the
            // switch, and the binding ignores that.
            await Task.Yield();
            if (current == generation)
            {
                pending = null;
                notify();
            }
        }
    }
}

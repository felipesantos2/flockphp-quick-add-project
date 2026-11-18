using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Features.Dumps;
using Flock.Tray.Features.FrankenPhp;
using Flock.Tray.Features.Mcp;
using Flock.Tray.Features.Mail;
using Flock.Tray.Features.Node;
using Flock.Tray.Features.Php;
using Flock.Tray.Features.Preferences;
using Flock.Tray.Features.Services;
using Flock.Tray.Features.Sites;
using Flock.Tray.Features.Update;
using Microsoft.Extensions.Logging.Abstractions;
using CatalogFeatureService = Flock.Tray.Features.Services.CatalogService;

namespace Flock.Tray.Infrastructure;

/// <summary>Owns daemon status, lifecycle actions, shell outcomes, and runtime intent.</summary>
public sealed class RuntimeService
{
    private readonly InstallationService installation;
    private readonly CliProcessRunner processRunner;
    private readonly StatusPollingService statusPolling;
    private readonly OperationRunner operations;
    private readonly IShellLauncher shell;
    private readonly OperationErrors operationErrors = new();
    private SecureLink? pendingSecure;
    private InstallLink? pendingInstall;

    public RuntimeService(FlockClient client, UpdateService updates, InstallationService installation, CliProcessRunner processRunner,
        StatusPollingService statusPolling, OperationRunner operations, MailService mail, PreferencesService preferences,
        ConfigurationService configuration, PhpService php, FrankenPhpService frankenPhp, NodeService node,
        ServicesService services, PostgresService postgres, CatalogFeatureService catalog, ServiceReleaseActions releaseActions, McpService mcp, DumpsService dumps,
        IUiDispatcher? dispatcher = null, SitesService? sites = null, TimeProvider? timeProvider = null, IShellLauncher? shell = null)
    {
        Client = client;
        UpdateService = updates;
        TimeProvider = timeProvider ?? TimeProvider.System;
        this.installation = installation;
        this.processRunner = processRunner;
        this.statusPolling = statusPolling;
        this.operations = operations;
        this.shell = shell ?? new ShellLauncher(NullLogger<ShellLauncher>.Instance);
        Mail = mail;
        Preferences = preferences;
        Configuration = configuration;
        Php = php;
        FrankenPhp = frankenPhp;
        Node = node;
        Services = services;
        Postgres = postgres;
        Catalog = catalog;
        ReleaseActions = releaseActions;
        Mcp = mcp;
        Dumps = dumps;
        Sites = sites ?? new SitesService(client, operations, dispatcher);
        Configuration.OnChange = RefreshAsync;
        Php.OnChange = RefreshSettingsAsync;
        FrankenPhp.OnChange = RefreshSettingsAsync;
        Node.OnChange = RefreshSettingsAsync;
        Sites.OnChange = RefreshSettingsAsync;
        Services.OnChange = RefreshAfterServicesMutationAsync;
        Postgres.OnChange = RefreshAfterServiceVersionMutationAsync;
        Catalog.OnChange = RefreshAfterServiceVersionMutationAsync;
        Sites.Changed += NotifyChanged;
        UpdateService.Changed += NotifyUpdateChanged;
        Dumps.OnCaptureChanged = RefreshAsync;
        statusPolling.StatusPublished = ApplyPublished;
        operations.RunningChanged += OperationRunningChanged;
    }

    public FlockClient Client { get; }
    public InstallationService Installation => installation;
    public IShellLauncher Shell => shell;
    public Status? Status { get; private set; }
    public string? Error { get; private set; }
    public string? ActionError { get; private set; }
    public string? RuntimeError { get; private set; }
    public bool IsRestarting { get; private set; }
    public bool NeedsSetup { get; set; }
    public InstallLink? PendingInstall
    {
        get => pendingInstall;
        set
        {
            if (pendingInstall == value)
            {
                return;
            }
            pendingInstall = value;
            NotifyChanged();
        }
    }
    public SecureLink? PendingSecure
    {
        get => pendingSecure;
        set
        {
            if (pendingSecure == value)
            {
                return;
            }
            pendingSecure = value;
            NotifyChanged();
        }
    }
    public bool IsUpdating { get; private set; }
    public string Headline => IsUpdating ? Strings.Menu_Status_Updating
        : NeedsSetup ? Strings.Menu_Status_NotSetUp
        : Status is { } status ? $"{(status.IsDegraded ? Strings.Menu_Status_NeedsAttention : Strings.Menu_Status_Running)} · v{status.Version}"
        : Strings.Menu_Status_NotRunning;
    public bool IsRunning => Status is not null;
    public bool IsDegraded => Status?.IsDegraded ?? false;
    public UpdateService UpdateService { get; }
    public TimeProvider TimeProvider { get; }
    public MailService Mail { get; }
    public DumpsService Dumps { get; }
    public PreferencesService Preferences { get; }
    public ConfigurationService Configuration { get; }
    public PhpService Php { get; }
    public FrankenPhpService FrankenPhp { get; }
    public NodeService Node { get; }
    public ServicesService Services { get; }
    public PostgresService Postgres { get; }
    public CatalogFeatureService Catalog { get; }
    public ServiceReleaseActions ReleaseActions { get; }
    public McpService Mcp { get; }
    public SitesService Sites { get; }
    public event Action? Changed;
    public event Action? UpdateChanged;

    public void NotifyChanged() => Changed?.Invoke();
    private void NotifyUpdateChanged() => UpdateChanged?.Invoke();

    public async Task UpdateInstallationAsync()
    {
        IsUpdating = true;
        NotifyChanged();
        var result = await installation.InstallAsync();
        IsUpdating = false;
        if (!result.Ok)
        {
            App.Log(new InvalidOperationException("flock install (update) failed:\n" + result.Output));
            Native.ShowError(Strings.Updates_CouldNotUpdate, Localization.Format(Strings.Updates_CouldNotUpdateDetails, result.Output));
        }
        NotifyChanged();
        await RefreshAsync();
    }

    public async Task RefreshAsync()
    {
        await statusPolling.RefreshAsync();
    }

    public async Task RefreshSetupAsync()
    {
        if (installation.IsInstalling || IsUpdating || installation.BundledFlock is null)
        {
            return;
        }
        var needed = !await installation.IsSetUpAsync();
        if (needed != NeedsSetup && !installation.IsInstalling && !IsUpdating)
        {
            NeedsSetup = needed;
            NotifyChanged();
        }
    }

    public Task SetXdebugAsync(bool enabled) => Configuration.UpdateAsync(new SettingsChange(Xdebug: enabled));
    public Task SetXdebugBreakpointsAsync(bool enabled) => Configuration.UpdateAsync(new SettingsChange(XdebugBreakpoints: enabled));
    public Task<OperationOutcome> SetDumpsAsync(bool enabled) => Dumps.SetCaptureAsync(enabled);

    public void DismissError()
    {
        if (ActionError is null && RuntimeError is null)
        {
            return;
        }
        ActionError = null;
        RuntimeError = null;
        NotifyChanged();
    }

    public async Task RestartAsync()
    {
        if (operations.IsRunning("lifecycle"))
        {
            return;
        }
        IsRestarting = true;
        NotifyChanged();
        var outcome = await operations.RunMutationAsync("lifecycle", operationErrors, async token =>
        {
            var result = await processRunner.RunAsync(FlockPaths.FlockBinary, ["restart"], TimeSpan.FromMinutes(2), token);
            if (!result.Succeeded)
            {
                throw new FlockException(result.Output.Length == 0 ? Localization.Format(Strings.App_CouldNotRestart, FlockPaths.FlockBinary) : result.Output);
            }
        }, CancellationToken.None);
        ActionError = outcome switch
        {
            OperationOutcome.Failed => operationErrors.Action,
            OperationOutcome.Succeeded => null,
            _ => ActionError,
        };
        IsRestarting = false;
        NotifyChanged();
        if (outcome is OperationOutcome.Succeeded or OperationOutcome.Failed)
        {
            await RefreshAsync();
        }
    }

    public ShellLaunchResult Open(string target) => ReportShellResult(shell.Open(target));
    public ShellLaunchResult Reveal(string path) => ReportShellResult(shell.Reveal(path));
    public ShellLaunchResult OpenConnection(string executable, string connection) => ReportShellResult(shell.OpenConnection(executable, connection));

    public void Open(Status.Site site)
    {
        if (site.Url is { Length: > 0 } url)
        {
            Open(url);
        }
    }

    public void ShowRuntimeError(string message)
    {
        if (RuntimeError != message)
        {
            RuntimeError = message;
            NotifyChanged();
        }
    }

    private ShellLaunchResult ReportShellResult(ShellLaunchResult result)
    {
        var changed = result.Succeeded ? RuntimeError is not null : RuntimeError != result.Message;
        RuntimeError = result.Succeeded ? null : result.Message;
        if (changed)
        {
            NotifyChanged();
        }
        return result;
    }

    private void ApplyPublished(Status? status)
    {
        if (status is null)
        {
            var changed = Error != statusPolling.LastError;
            Error = statusPolling.LastError;
            Status = null;
            if (changed)
            {
                NotifyChanged();
            }
        }
        else
        {
            Apply(status);
        }
    }

    private void OperationRunningChanged(string key, bool running)
    {
        if (running)
        {
            statusPolling.BeginMutation();
        }
        else
        {
            statusPolling.EndMutation();
        }
    }

    private void Apply(Status fresh)
    {
        var changed = !string.Equals(FlockJson.Encode(Status), FlockJson.Encode(fresh), StringComparison.Ordinal) || Error is not null;
        var previous = Status;
        Status = fresh;
        Error = null;
        if (fresh.Channel != Preferences.Snapshot.Channel)
        {
            _ = Preferences.UpdateAsync(value => value with { Channel = fresh.Channel });
        }
        if (previous?.MailCount != fresh.MailCount)
        {
            _ = Mail.RefreshIfVisibleAsync();
        }
        if (previous?.DumpCount != fresh.DumpCount || previous?.EventCount != fresh.EventCount)
        {
            _ = Dumps.RefreshIfVisibleAsync();
        }
        if (changed)
        {
            NotifyChanged();
        }
    }

    private async Task RefreshSettingsAsync() => await Task.WhenAll(Configuration.RefreshAsync(), Services.RefreshAsync(), RefreshAsync());
    private Task RefreshAfterServicesMutationAsync() => Task.WhenAll(Configuration.RefreshAsync(), RefreshAsync());
    private Task RefreshAfterServiceVersionMutationAsync() => Task.WhenAll(Configuration.RefreshAsync(), Services.RefreshAsync(), RefreshAsync());
}

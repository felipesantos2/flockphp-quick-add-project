using System.Diagnostics;
using System.Globalization;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.Settings;
using Flock.Tray.ViewModels;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using WinRT;
namespace Flock.Tray;

public enum WindowKind { Mail, Dumps, Logs, Settings, Setup }

/// <summary>
///     The tray app: a notification area icon with a panel (the macOS menu bar extra), plus the
///     Mail, Dumps, Logs and Settings windows. It keeps running with no window open.
/// </summary>
public sealed partial class App : Application
{

    /// <summary>Command line argument of the sign-in autostart.</summary>
    public const string BackgroundArgument = "--background";
    private readonly AcceptedInstallTracker acceptedInstalls = new(Log);
    private DispatcherQueue? dispatcher;

    private AppHost? host;
    private Task? launchTask;
    private IPanelWindow? panel;
    private IPanelWindowFactory? panelFactory;
    private Task? quitTask;
    private ITrayIcon? tray;
    private ITrayIconFactory? trayFactory;
    private IUiDispatcher? uiDispatcher;
    private Task? updateTask;
    private bool updatingTray;
    private bool relaunching;
    private WindowManager? windowManager;

    /// <summary>Notifies of worktree setups that fail while no progress dialog follows them.</summary>
    internal WorktreeNotifier WorktreeNotifier { get; } = new();

    public App() : this(new AppOptions())
    {
    }

    internal App(AppOptions options)
    {
        InitializeComponent();
        Options = options;
        if (options.Theme is { } theme)
        {
            RequestedTheme = theme;
        }
        UnhandledException += (_, args) =>
        {
            Log(args.Exception);
            if (host is not null)
            {
                Runtime.RequestQuit();
            }
            else
            {
                Native.ShowError(Strings.App_CouldNotStart, Localization.Format(Strings.App_CouldNotStartDetails, args.Exception.Message));
            }
            // An unowned WinUI exception is fatal. Do not mark it handled just to keep the tray alive.
            args.Handled = false;
        };
    }

    public new static App Current => (App)Application.Current;

    public RuntimeService State => host?.RuntimeService ?? throw new InvalidOperationException("The application has not launched.");

    public ApplicationRuntime Runtime => host?.Runtime ?? throw new InvalidOperationException("The application has not launched.");

    internal AppOptions Options { get; }

    internal WindowManager Windows => windowManager ?? throw new InvalidOperationException("The application has not launched.");

    internal void StartAcceptedInstall(Func<CancellationToken, Task> install)
    {
        acceptedInstalls.Start(install);
    }

    internal static void Run(AppOptions options)
    {
        ComWrappersSupport.InitializeComWrappers();
        Start(callback =>
        {
            SynchronizationContext.SetSynchronizationContext(new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread()));
            _ = new App(options);
        });
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        if (launchTask is null)
        {
            launchTask = StartAsync(DispatcherQueue.GetForCurrentThread());
        }
    }

    private async Task StartAsync(DispatcherQueue dispatcher)
    {
        try
        {
            this.dispatcher = dispatcher;
            Resources.MergedDictionaries.Add(new XamlControlsResources());
            DispatcherShutdownMode = DispatcherShutdownMode.OnExplicitShutdown;
            host = AppHost.Create(dispatcher, ReleaseUiAsync, Options);
            await host.StartAsync();
            windowManager = host.GetService<WindowManager>();
            uiDispatcher = host.GetService<IUiDispatcher>();
            trayFactory = host.GetService<ITrayIconFactory>();
            panelFactory = host.GetService<IPanelWindowFactory>();
            if (Options.OnStarted is { } onStarted)
            {
                await onStarted(this);
                return;
            }

            tray = trayFactory is null
                ? throw new InvalidOperationException("The tray icon factory is unavailable.")
                : await trayFactory.CreateAsync(IconPath(), Options.Identity.DisplayName);
            tray.Clicked += TogglePanel;
            tray.ShowRequested += ShowMain;
            tray.LinkReceived += OpenLink;
            tray.MenuRequested += ShowMenu;
            tray.ThemeChanged += UpdateTray;
            tray.Failure += TrayFailure;
            State.Changed += UpdateTray;
            WorktreeNotifier.Start(State, dispatcher, tray);
            State.NeedsSetup = State.Installation.BundledFlock is not null && !await State.Installation.IsSetUpAsync();

            if (!Options.Identity.IsDevelopment && !State.NeedsSetup && await State.Installation.NeedsUpdateAsync())
            {
                host.StartUpdateInstallation();
            }
            var arguments = Environment.GetCommandLineArgs();
            if (arguments.FirstOrDefault(SecureLink.IsFlockLink) is { } link)
            {
                OpenLink(link);
            }
            else if (arguments.Any(argument => argument.StartsWith(Program.RelaunchArgument, StringComparison.Ordinal)))
            {
                // Back where the restart was asked for (Settings → General, or the setup before Flock is set up).
                Open(WindowKind.Settings, SettingsPane.General);
            }
            else if (!arguments.Contains(BackgroundArgument))
            {
                ShowMain();
            }
        }
        catch (Exception error)
        {
            Log(error);
            Native.ShowError(Strings.App_CouldNotStart, Localization.Format(Strings.App_CouldNotStartDetails, error.Message));
            await QuitAsync();
        }
    }

    private string IconPath()
    {
        return TrayIconPath(State, Native.TaskbarIsLight);
    }

    /// <summary>The logomark for the daemon's state, colored for a light or a dark background.</summary>
    /// <param name="extension">.ico for the notification area; .png inside windows (WinUI scales up an .ico's smallest image).</param>
    public static string TrayIconPath(RuntimeService model, bool light, string extension = ".ico")
    {
        var state = !model.IsRunning || model.NeedsSetup ? "TrayStopped" : model.IsDegraded ? "TrayWarning" : "Tray";
        return Path.Combine(AppContext.BaseDirectory, "Assets", $"{state}.{(light ? "Light" : "Dark")}{extension}");
    }

    private void UpdateTray()
    {
        if (updatingTray)
        {
            return;
        }
        updatingTray = true;
        try
        {
            UpdateTrayCore();
        }
        finally
        {
            updatingTray = false;
        }
    }

    private void UpdateTrayCore()
    {
        var tooltip = Options.Identity.DisplayName + " — " + State.Headline;
        if (State.Status is { Warnings.Count: > 0 } status && !State.NeedsSetup)
        {
            tooltip += "\n" + status.Warnings[0];
        }
        tray?.Update(IconPath(), tooltip);
    }

    private void TogglePanel()
    {
        panel ??= panelFactory?.Create() ?? throw new InvalidOperationException("The panel window factory is unavailable.");
        panel.Toggle();
    }

    public void ShowPanel()
    {
        panel ??= panelFactory?.Create() ?? throw new InvalidOperationException("The panel window factory is unavailable.");
        panel.ShowNearTray();
    }

    /// <summary>Opens the app from outside the tray (a start by hand): the setup until Flock is set up, then Settings.</summary>
    private void ShowMain()
    {
        Open(State.NeedsSetup ? WindowKind.Setup : WindowKind.Settings);
    }

    /// <summary>
    ///     A flock:// link (registered by the installer). flock://install, e.g. from the website's
    ///     install buttons, shows the service in Settings → Services (a PHP or Node.js version in
    ///     Settings → PHP or Node), which asks before installing anything; before the setup the link
    ///     waits, and the pane asks about it once Flock is set up.
    ///     flock://secure, from the page served for a site that isn't secured, asks in Settings → Sites.
    ///     flock://update, from `flock update`, installs an update of the app in Settings → General
    ///     (before the setup without opening Settings).
    /// </summary>
    private void OpenLink(string? text)
    {
        if (UpdateLink.IsUpdateLink(text))
        {
            InstallUpdate(check: true);
            return;
        }
        if (SecureLink.Parse(text) is { } secure && !State.NeedsSetup)
        {
            State.PendingSecure = secure;
            Open(WindowKind.Settings, SettingsPane.Sites);
            return;
        }
        if (InstallLink.Parse(text) is not { } link)
        {
            ShowMain();
            return;
        }
        State.PendingInstall = link;
        if (State.NeedsSetup)
        {
            Open(WindowKind.Setup);
            return;
        }
        ShowInstall(link);
    }

    /// <summary>
    ///     Opens the pane that asks about an install link: PHP and Node for their versions, Services
    ///     (on the service's details) for everything else.
    /// </summary>
    public void ShowInstall(InstallLink link)
    {
        switch (link.Service)
        {
            case InstallLink.Php:
                Open(WindowKind.Settings, SettingsPane.Php);
                break;
            case InstallLink.Node:
                Open(WindowKind.Settings, SettingsPane.Node);
                break;
            default:
                OpenService(link.Service);
                break;
        }
    }

    private void OpenService(string service)
    {
        panel?.Hide();
        var kind = State.NeedsSetup ? WindowKind.Setup : WindowKind.Settings;
        _ = OpenCoreAsync(kind, SettingsPane.Services, service);
    }

    private void ShowMenu()
    {
        if (tray is null)
        {
            return;
        }
        // While an update is available it follows the first item; the rest keep their order.
        var update = State.UpdateService.Available is { } available && !State.UpdateService.IsBusy
            ? Localization.Format(Strings.Menu_UpdateTo, available.Version) : null;
        // Before the setup there is nothing to open yet: Mail, Dumps, Logs and Settings all need the daemon.
        List<string?> items = State.NeedsSetup
            ? [Strings.Menu_SetUpFlock, null, Strings.Menu_QuitFlock]
            : [Strings.Menu_OpenFlock, null, Strings.Menu_Mail, Strings.Menu_Dumps, Strings.Menu_Logs, Strings.Menu_Settings, null,
                State.IsRunning ? Strings.Menu_RestartFlock : Strings.Menu_StartFlock, Strings.Menu_QuitFlock];
        if (update is not null)
        {
            items.Insert(1, update);
        }
        var choice = tray.ShowMenu([.. items], 0);
        if (update is not null && choice >= 1)
        {
            if (choice == 1)
            {
                InstallUpdate();
                return;
            }
            choice--;
        }
        if (State.NeedsSetup)
        {
            switch (choice)
            {
                case 0: Open(WindowKind.Setup); break;
                case 2: Quit(); break;
            }
            return;
        }
        switch (choice)
        {
            case 0: ShowPanel(); break;
            case 2: Open(WindowKind.Mail); break;
            case 3: Open(WindowKind.Dumps); break;
            case 4: Open(WindowKind.Logs); break;
            case 5: Open(WindowKind.Settings); break;
            case 7: _ = State.RestartAsync(); break;
            case 8: Quit(); break;
        }
    }

    /// <summary>
    ///     Installs the available update, showing it in Settings → General once Flock is set up. Before
    ///     the setup the silent installer runs on its own: Settings has nothing to show yet.
    /// </summary>
    public void InstallUpdate(bool check = false)
    {
        if (updateTask is { IsCompleted: false })
        {
            return;
        }
        if (!State.NeedsSetup)
        {
            Open(WindowKind.Settings, SettingsPane.General);
        }
        updateTask = ObserveUpdateAsync(check);
    }

    private async Task ObserveUpdateAsync(bool check)
    {
        try
        {
            await (check ? State.UpdateService.CheckAndInstallAsync() : State.UpdateService.InstallAsync());
        }
        catch (Exception error)
        {
            Log(error);
        }
    }

    /// <summary>Opens a window, or brings it to the front when it is already open. Until Flock is set up, that is the setup.</summary>
    public void Open(WindowKind kind, SettingsPane? pane = null)
    {
        panel?.Hide();
        if (kind == WindowKind.Logs && windowManager is not null)
        {
            _ = OpenLogsCoreAsync();
            return;
        }
        if (State.NeedsSetup)
        {
            kind = WindowKind.Setup;
        }
        _ = OpenCoreAsync(kind, pane);
    }

    private async Task OpenCoreAsync(WindowKind kind, SettingsPane? pane, string? service = null)
    {
        try
        {
            if (windowManager is null)
            {
                return;
            }
            var window = await windowManager.OpenAsync(kind);
            if (pane is { } selected && window is Settings.SettingsWindow settings)
            {
                settings.Select(selected);
                if (service is not null)
                {
                    settings.ShowService(service);
                }
            }
            ((FlockWindow)window).Present();
        }
        catch (Exception error)
        {
            Log(error);
        }
    }

    /// <summary>Opens Settings → Sites with a site (or worktree) selected.</summary>
    public void OpenSite(string name)
    {
        Open(WindowKind.Settings, SettingsPane.Sites);
        if (windowManager?.Open(WindowKind.Settings) is SettingsWindow settings && !State.NeedsSetup)
        {
            settings.Sites.Select(name);
        }
    }

    /// <summary>Opens the Logs window on one log.</summary>
    public void OpenLog(LogsViewModel.Source source)
    {
        _ = OpenLogsCoreAsync(source);
    }

    private async Task OpenLogsCoreAsync(LogsViewModel.Source? source = null)
    {
        try
        {
            panel?.Hide();
            var kind = State.NeedsSetup ? WindowKind.Setup : WindowKind.Logs;
            var window = windowManager is null ? null : await windowManager.OpenAsync(kind);
            if (source is not null && window is LogsWindow logs)
            {
                await logs.ShowSourceAsync(source);
            }
            if (window is FlockWindow flockWindow)
            {
                flockWindow.Present();
            }
        }
        catch (Exception error)
        {
            Log(error);
        }
    }

    public void Quit()
    {
        _ = QuitAsync();
    }

    /// <summary>
    /// Quits and starts again (a new language applies only to a new process), false when the new
    /// process couldn't start. The new process waits for this one to exit, however long quitting takes,
    /// before it takes the single-instance mutex; the daemon keeps running throughout.
    /// </summary>
    public bool Relaunch()
    {
        if (relaunching)
        {
            return true;
        }
        var start = new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = false };
        start.ArgumentList.Add(Program.RelaunchArgument + Environment.ProcessId.ToString(CultureInfo.InvariantCulture));
        if (Environment.GetCommandLineArgs().Contains("--dev"))
        {
            start.ArgumentList.Add("--dev");
        }
        try
        {
            using var process = Process.Start(start);
        }
        catch (Exception error) when (error is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            Log(error);
            State.ShowRuntimeError(Localization.Format(Strings.App_CouldNotRelaunch, error.Message));
            return false;
        }
        relaunching = true;
        Quit();
        return true;
    }

    public Task QuitAsync()
    {
        if (quitTask is not null)
        {
            return quitTask;
        }
        return quitTask = QuitCoreAsync();
    }

    private async Task QuitCoreAsync()
    {
        WorktreeNotifier.Stop();
        try
        {
            await acceptedInstalls.StopAsync();
            if (host is not null)
            {
                await host.QuitAsync();
            }
            else
            {
                await ReleaseUiAsync();
            }
        }
        catch (Exception error)
        {
            Log(error);
        }
        if (dispatcher is { HasThreadAccess: true })
        {
            Exit();
        }
        else
        {
            dispatcher?.TryEnqueue(Exit);
        }
    }

    /// <summary>Appends an unexpected error to %LocalAppData%\Flock\tray.log.</summary>
    public static void Log(Exception e)
    {
        if (Application.Current is App { host: { } appHost } && appHost.GetService<FileLogSink>() is { } sink)
        {
            sink.TryWrite($"{DateTimeOffset.Now:O} fatal: {e}");
            return;
        }
        FileLogSink.WriteEmergency(e);
    }

    private async Task ReleaseUiAsync()
    {
        if (uiDispatcher is not null)
        {
            await uiDispatcher.InvokeAsync(ReleaseUiOnDispatcherAsync, CancellationToken.None);
            return;
        }
        await ReleaseUiOnDispatcherAsync();
    }

    private async Task ReleaseUiOnDispatcherAsync()
    {
        if (windowManager is not null)
        {
            await windowManager.CloseAllAsync();
        }
        panel?.Close();
        panel = null;
        if (tray is not null)
        {
            tray.Clicked -= TogglePanel;
            tray.ShowRequested -= ShowMain;
            tray.LinkReceived -= OpenLink;
            tray.MenuRequested -= ShowMenu;
            tray.ThemeChanged -= UpdateTray;
            tray.Failure -= TrayFailure;
        }
        tray?.Dispose();
        tray = null;
    }

    private void TrayFailure(string message)
    {
        State.ShowRuntimeError(message);
    }
}
using System.Diagnostics;
using System.Globalization;
using Flock.Kit;
using Flock.Tray.Features.Preferences;
using Flock.Tray.Infrastructure;

namespace Flock.Tray;

/// <summary>
/// Entry point: `FlockTray --selftest` checks selected daemon endpoints and exits (the output goes
/// to the console it was started from); otherwise the tray app starts, once.
/// </summary>
public static class Program
{
    /// <summary>
    /// Prefix of the argument of a restart from Settings (a new language), followed by the old instance's
    /// process id: "--relaunch=1234". The new instance waits for that process to exit.
    /// </summary>
    public const string RelaunchArgument = "--relaunch=";

    [STAThread]
    private static int Main(string[] args)
    {
        if (args.Contains("--selftest"))
        {
            Native.AttachParentConsole();
            return RunSelfTestAsync().GetAwaiter().GetResult();
        }

        var identity = LaunchIdentity.ForExecutable(Environment.ProcessPath!, Installation.Uninstaller is not null,
            args.Contains("--dev"));
        // Installed launches share one instance; local builds only activate their own executable.
        using var single = new Mutex(initiallyOwned: true, identity.MutexName, out var first);
        if (!first && !(RelaunchedFrom(args) is { } previous && WaitForPreviousInstance(single, previous)))
        {
            Native.SignalRunningInstance(args.FirstOrDefault(SecureLink.IsFlockLink), identity.WindowClass);
            return 0;
        }

        // Unpackaged apps would put WebView2's profile next to the exe, which may not be writable.
        Environment.SetEnvironmentVariable("WEBVIEW2_USER_DATA_FOLDER",
            Path.Combine(identity.DataDirectory, "WebView2"));

        var preferencesPath = Path.Combine(identity.DataDirectory, "tray.json");
        // Before any window exists: XAML and code read their text in this language.
        AppLanguage.Apply(new PreferencesService(preferencesPath, log: FileLogSink.WriteEmergency).LoadAsync().GetAwaiter().GetResult().Language);

        try
        {
            App.Run(new AppOptions
            {
                Identity = identity,
                PreferencesPath = preferencesPath,
            });
        }
        catch (Exception e)
        {
            // Without this, a failure while starting up just makes the app vanish.
            App.Log(e);
            Native.ShowError(Strings.App_CouldNotStart, Localization.Format(Strings.App_CouldNotStartDetails, e.Message));
            return 1;
        }
        return 0;
    }

    /// <summary>The process id of the instance that relaunched this one, or null.</summary>
    private static int? RelaunchedFrom(string[] args) =>
        args.FirstOrDefault(arg => arg.StartsWith(RelaunchArgument, StringComparison.Ordinal)) is { } arg
        && int.TryParse(arg.AsSpan(RelaunchArgument.Length), NumberStyles.None, CultureInfo.InvariantCulture, out var id) ? id : null;

    /// <summary>
    /// Takes over the single-instance mutex once the instance that relaunched this one has exited. Quitting
    /// waits for running installs and worktree setups, so there is no time limit. False when another
    /// instance holds the mutex by then; that one gets activated instead.
    /// </summary>
    private static bool WaitForPreviousInstance(Mutex single, int previous)
    {
        try
        {
            using var process = Process.GetProcessById(previous);
            process.WaitForExit();
        }
        catch (Exception error) when (error is ArgumentException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            // It has exited already (or can't be watched; then the mutex below decides).
        }
        try
        {
            return single.WaitOne(0);
        }
        catch (AbandonedMutexException)
        {
            // The old instance exited without releasing it: this one owns it now.
            return true;
        }
    }

    private static async Task<int> RunSelfTestAsync()
    {
        await using var host = AppHost.Create();
        await host.StartAsync();
        return await SelfTest.RunAsync(host.Client, Console.Out);
    }
}

using System.Diagnostics;
using System.Globalization;

namespace Flock.Tray;

/// <summary>
/// `Flock.Screenshots --out &lt;dir&gt; [--only id,id]` renders Screenshots.All with the demo data in
/// tools/screenshots/fixtures into &lt;dir&gt;\{id}-windows-{light|dark}.png. WinUI fixes an app's theme
/// when it starts, so each theme runs in a process of its own (--theme).
/// </summary>
public static class ScreenshotProgram
{
    [STAThread]
    private static int Main(string[] args)
    {
        var options = Screenshots.Options.Parse(args);
        if (options is null)
        {
            Console.Error.WriteLine("usage: Flock.Screenshots --out <dir> [--only id,id] [--theme light|dark]");
            return 2;
        }
        if (options.Theme is null)
        {
            return RunEachTheme(args);
        }

        // Dates, numbers and sizes look the same on every machine, and the text is English (the app's
        // Language preference is applied by FlockTray's own Main only).
        CultureInfo.DefaultThreadCurrentCulture = CultureInfo.DefaultThreadCurrentUICulture = CultureInfo.GetCultureInfo("en-US");
        CultureInfo.CurrentCulture = CultureInfo.CurrentUICulture = CultureInfo.GetCultureInfo("en-US");

        // FlockPaths and with it api.json point into a scratch folder, so the app talks to the fake daemon only.
        var home = Directory.CreateTempSubdirectory("flock-screenshots-");
        Environment.SetEnvironmentVariable("FLOCK_HOME", home.FullName);
        Environment.SetEnvironmentVariable("WEBVIEW2_USER_DATA_FOLDER", Path.Combine(home.FullName, "WebView2"));
        using var daemon = new FakeDaemon(Path.Combine(AppContext.BaseDirectory, "fixtures"), home.FullName);
        var screenshots = new Screenshots(options);

        App.Run(new AppOptions
        {
            Theme = screenshots.Theme,
            UseSolidBackground = true,
            KeepPanelOpen = true,
            EnableBackgroundServices = false,
            PreferencesPath = Path.Combine(home.FullName, "tray.json"),
            OnStarted = screenshots.RunAsync,
        });

        try
        {
            home.Delete(recursive: true);
        }
        catch (IOException)
        {
            // WebView2 may still hold its profile for a moment; the temp folder gets cleaned up eventually.
        }
        return screenshots.Failures;
    }

    /// <summary>Runs this program once per theme with the same arguments.</summary>
    private static int RunEachTheme(string[] args)
    {
        var failures = 0;
        foreach (var theme in new[] { "light", "dark" })
        {
            var start = new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = false };
            foreach (var arg in args)
            {
                start.ArgumentList.Add(arg);
            }
            start.ArgumentList.Add("--theme");
            start.ArgumentList.Add(theme);
            using var process = Process.Start(start)!;
            process.WaitForExit();
            failures += process.ExitCode;
        }
        return failures;
    }
}

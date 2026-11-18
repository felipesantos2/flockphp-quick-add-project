using Flock.Kit;
using Flock.Tray.Features.Preferences;
using Microsoft.Win32;

namespace Flock.Tray.Infrastructure;

/// <summary>Owns local setup admission while the daemon-owned install job continues after UI close.</summary>
public sealed class InstallationService(CliProcessRunner processRunner, PreferencesService preferences, IUiDispatcher? dispatcher = null)
{
    private readonly object gate = new();
    private Task<Installation.Result>? setup;

    public bool IsInstalling => setup is { IsCompleted: false };
    public Task<List<Conflict>> ConflictsAsync() => Installation.ConflictsAsync(processRunner);

    public Task<Installation.Result> InstallAsync(Action<string>? onLine = null, CancellationToken cancellationToken = default, bool retry = false)
    {
        lock (gate)
        {
            if (setup is not null && !retry)
            {
                return setup;
            }
            setup = InstallCoreAsync(onLine, cancellationToken);
            return setup;
        }
    }

    private async Task<Installation.Result> InstallCoreAsync(Action<string>? onLine, CancellationToken cancellationToken)
    {
        if (Installation.BundledFlock is not { } bundled)
        {
            return new Installation.Result(false, Strings.Setup_NoBundledCli);
        }
        var result = await processRunner.RunAsync(bundled, ["install"], cancellationToken: cancellationToken);
        await RecordInstalledAsync(cancellationToken);
        if (result.Output.Length > 0)
        {
            foreach (var line in result.Output.Split('\n'))
            {
                var text = line.TrimEnd('\r');
                if (onLine is not null)
                {
                    if (dispatcher is null)
                    {
                        onLine(text);
                    }
                    else
                    {
                        await dispatcher.InvokeAsync(() => onLine(text), CancellationToken.None);
                    }
                }
            }
        }
        return new Installation.Result(result.Succeeded, result.Output);
    }

    public async Task<bool> IsSetUpAsync(CancellationToken cancellationToken = default)
    {
        if (!File.Exists(FlockPaths.FlockBinary))
        {
            return false;
        }
        using (var key = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Run"))
        {
            if (key?.GetValue("Flock Daemon") is string)
            {
                return true;
            }
        }
        var result = await processRunner.RunAsync("schtasks", ["/Query", "/TN", "Flock Daemon"], TimeSpan.FromSeconds(10), cancellationToken);
        return result.Succeeded;
    }

    public string? BundledFlock => Installation.BundledFlock;

    public async Task<bool> NeedsUpdateAsync(CancellationToken cancellationToken = default) =>
        Installation.NeedsUpdate(await preferences.LoadAsync(cancellationToken));

    public async Task RecordInstalledAsync(CancellationToken cancellationToken = default)
    {
        if (Installation.InstalledHash() is { } hash)
        {
            await preferences.UpdateAsync(value => value with { InstalledFlock = hash }, cancellationToken);
        }
    }

    public Task DrainAsync()
    {
        lock (gate)
        {
            return setup ?? Task.CompletedTask;
        }
    }
}

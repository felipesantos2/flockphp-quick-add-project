using System.Diagnostics;
using System.Security.Cryptography;
using Flock.Tray.Features.Preferences;
using Flock.Kit;
using Microsoft.Win32;

namespace Flock.Tray;

/// <summary>
/// Setting Flock up from the app. The installer puts flock.exe and flock-helper.exe next to
/// FlockTray.exe; `flock install` copies them to %AppData%\Flock\bin, registers the daemon and,
/// with one UAC prompt, the hosts helper and the CA. Development builds run without a bundled CLI
/// and leave installing to the terminal, as before.
/// </summary>
public static class Installation
{
    /// <summary>The CLI shipped with this app, or null for a development build.</summary>
    public static string? BundledFlock =>
        Path.Combine(AppContext.BaseDirectory, "flock.exe") is var path && File.Exists(path) ? path : null;

    /// <summary>The Inno Setup uninstaller, present when the app was installed by Flock-Setup.exe.</summary>
    public static string? Uninstaller =>
        Path.Combine(AppContext.BaseDirectory, "unins000.exe") is var path && File.Exists(path) ? path : null;


    /// <summary>
    /// The installed CLI should be replaced by the bundled one: it differs, and the installed one is
    /// what this app installed last time (or nothing is recorded yet). A CLI installed by hand from
    /// another build is left alone.
    /// </summary>
    public static bool NeedsUpdate(PreferenceData preferences)
    {
        if (BundledFlock is not { } bundled || !File.Exists(FlockPaths.FlockBinary))
        {
            return false;
        }
        var installed = Hash(FlockPaths.FlockBinary);
        if (installed == Hash(bundled))
        {
            return false;
        }
        var recorded = preferences.InstalledFlock;
        return string.IsNullOrEmpty(recorded) || recorded == installed;
    }

    /// <summary>
    /// Remembers the CLI this app installed, so later updates can tell it from a hand-installed one.
    /// After every `flock install`, also a failed one: it copies the CLI first, so one that failed
    /// later (e.g. at a declined UAC prompt) still installed it.
    /// </summary>
    public static string? InstalledHash()
    {
        return BundledFlock is { } bundled && File.Exists(FlockPaths.FlockBinary) && Hash(FlockPaths.FlockBinary) is { Length: > 0 } hash
            && hash == Hash(bundled) ? hash : null;
    }

    /// <summary>
    /// Asks the bundled CLI which other programs use Flock's ports. Empty when there are none, and
    /// when the check itself fails: `flock install` checks again and reports them.
    /// </summary>
    public static async Task<List<Conflict>> ConflictsAsync(Infrastructure.CliProcessRunner runner)
    {
        if (BundledFlock is not { } bundled)
        {
            return [];
        }
        var result = await runner.RunAsync(bundled, new[] { "conflicts", "--json" }, TimeSpan.FromSeconds(30));
        try
        {
            return result.Succeeded ? FlockJson.Decode<ConflictList>(result.Output).Conflicts : [];
        }
        catch (System.Text.Json.JsonException)
        {
            return [];
        }
    }

    public sealed record Result(bool Ok, string Output);

    /// <summary>
    /// Starts uninstalling: the installer's uninstaller (which runs `flock uninstall`, then removes
    /// the app) when there is one. purge also deletes the user's settings and databases.
    /// Returns false when there is no uninstaller (a development build).
    /// </summary>
    public static bool StartUninstaller(bool purge)
    {
        if (Uninstaller is not { } uninstaller)
        {
            return false;
        }
        // /SILENT skips the uninstaller's own "Are you sure" question; the app asked already.
        Process.Start(new ProcessStartInfo(uninstaller, purge ? "/SILENT /PURGE" : "/SILENT") { UseShellExecute = true });
        return true;
    }

    private static string Hash(string path)
    {
        try
        {
            using var stream = File.OpenRead(path);
            return Convert.ToHexString(SHA256.HashData(stream));
        }
        catch (IOException)
        {
            return "";
        }
    }

}

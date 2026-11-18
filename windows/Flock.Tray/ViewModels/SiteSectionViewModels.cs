using System.Globalization;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Sites;

namespace Flock.Tray.ViewModels;

/// <summary>
///     The Share section: the site at a temporary public URL through a Cloudflare quick tunnel, to show
///     work in progress. The daemon builds the assets when they need it, starts the tunnel and ends it
///     after 8 hours; the status follows it through building, starting, running or failed.
/// </summary>
internal sealed partial class ShareSectionViewModel(ISiteDetailHost host) : DetailViewModel
{
    private Status.Site site = new();
    private Status.SiteShare? share;

    public bool IsIdle => share is null;
    public bool CanShare => site.Url is not null;
    public bool IsStarting => share?.State is Status.ShareState.Building or Status.ShareState.Starting;
    public string StartingText => share?.State == Status.ShareState.Building ? Strings.Sites_Share_Building : Strings.Sites_Share_Starting;
    public bool IsRunning => share is { State: Status.ShareState.Running, Url: not null };
    public bool IsStopped => share is not null && !IsStarting && !IsRunning;

    public string Url => share?.Url ?? "";
    public string Qr => IsRunning && share?.Qr is { Length: > 0 } data ? data : "";
    public bool HasQr => Qr.Length > 0;
    public string Expires => share?.ExpiresAt is { } expires ? Localization.Format(Strings.Sites_Share_EndsAt, expires.ToLocalTime().ToString("t", CultureInfo.CurrentCulture)) : "";
    public string Allowed => share is { Allow.Count: > 0 } ? Localization.Format(Strings.Sites_Share_OnlyFor, string.Join(", ", share.Allow)) : "";
    public bool IsDebug => share?.Debug == true;
    public string Warning => share?.Warning ?? "";
    public bool HasWarning => share?.Warning is not null;
    public string RebuildError => share?.RebuildError ?? "";
    public bool HasRebuildError => share?.RebuildError is not null;
    public bool CanRebuild => share?.CanRebuild == true;
    public string RebuildText => share?.Rebuilding == true ? Strings.Sites_Share_Rebuilding : Strings.Sites_Share_Rebuild;
    public bool IsRebuildEnabled => share?.Rebuilding != true;
    public string Failure => share?.Error ?? Strings.Sites_Share_Stopped;
    public string StopText => share?.IsRunning == true ? Strings.Sites_Share_StopSharing : share?.IsFailed == true ? Strings.Sites_Share_Dismiss : Strings.Sites_Share_Stop;

    /// <summary>The site the QR code is saved for.</summary>
    public string SiteName => site.Name;

    public void Update(Status.Site value)
    {
        site = value;
        share = value.Share;
        Refresh();
    }

    [RelayCommand]
    private void Start()
    {
        var current = site;
        host.Start(current.Name, () => host.StartShareAsync(current));
    }

    [RelayCommand]
    private void Stop()
    {
        var name = site.Name;
        host.Start(name, () => host.Sites.StopShareAsync(name));
    }

    [RelayCommand]
    private void Rebuild()
    {
        var name = site.Name;
        host.Start(name, () => host.Sites.RebuildShareAsync(name));
    }

    [RelayCommand]
    private void TryAgain()
    {
        var name = site.Name;
        var allow = share?.Allow.ToList() ?? [];
        var debug = share?.Debug == true;
        host.Start(name, () => host.Sites.StartShareAsync(name, allow, debug));
    }

    [RelayCommand]
    private void Open()
    {
        if (share?.Url is { } url)
        {
            host.OpenUrl(url);
        }
    }

    [RelayCommand]
    private void Copy()
    {
        if (share?.Url is { } url)
        {
            host.Copy(url);
        }
    }

    [RelayCommand]
    private void CopyQr()
    {
        if (Qr is { Length: > 0 } data)
        {
            _ = host.CopyQrAsync(data);
        }
    }

    [RelayCommand]
    private void SaveQr()
    {
        if (Qr is { Length: > 0 } data)
        {
            _ = host.SaveQrAsync(data, site.Name);
        }
    }
}

/// <summary>
///     What runs a site's PHP: php-cgi, or FrankenPHP (classic or Laravel Octane's worker mode), with
///     FrankenPHP's state, Restart and its log.
/// </summary>
internal sealed partial class SiteServerSectionViewModel(ISiteDetailHost host) : DetailViewModel
{
    private Status.Site site = new();
    private SiteRowViewModel? row;
    private bool franken;
    private bool octane;
    private bool restarting;
    private int serverShown;
    private int modeShown;

    public int ServerIndex
    {
        get => serverShown;
        set
        {
            if (Refreshing || value < 0)
            {
                return;
            }
            serverShown = value;
            if (value == 1 == franken)
            {
                return;
            }
            var change = value == 1 ? Status.SiteServer.FrankenPhp : Status.SiteServer.FastCgi;
            var (name, draft) = (site.Name, row);
            host.Start(name, () => host.UpdateSiteAsync(name, new SiteChange(Server: change), () => draft?.SetDraftServer(change)), false);
        }
    }

    /// <summary>FrankenPHP needs PHP 8.2; a site already on it keeps the option (the daemon explains the fallback).</summary>
    public bool CanPickFrankenPhp => franken || SupportsFrankenPhp(site.Php);

    public bool IsFrankenPhp => franken;

    public int ModeIndex
    {
        get => modeShown;
        set
        {
            if (Refreshing || value < 0)
            {
                return;
            }
            modeShown = value;
            if (value == 1 == octane)
            {
                return;
            }
            var worker = value == 1 ? Status.SiteServer.Octane : "";
            var (name, draft) = (site.Name, row);
            host.Start(name, () => host.UpdateSiteAsync(name, new SiteChange(Worker: worker), () => draft?.SetDraftWorker(worker)), false);
        }
    }

    public bool CanPickOctane => octane || site.OctaneAvailable;
    public string? ModeTooltip => site.OctaneAvailable ? null : Strings.Sites_OctaneTooltip;

    // FrankenPHP's state: running, downloading, or why php-cgi serves the site meanwhile.
    private Status.SiteFrankenPhp? State => franken ? site.FrankenPhp : null;
    public bool IsRunning => State is { IsRunning: true };
    public string RunningText => State is { } state
        ? Localization.Format(site.Worker == Status.SiteServer.Octane ? Strings.Sites_FrankenphpRunningWorker : Strings.Sites_FrankenphpRunning, state.Version, state.PhpVersion)
        : "";
    public bool IsInstalling => State is { IsRunning: false, IsInstalling: true };
    public string InstallingText => State is { HasProgress: true } state
        ? Localization.Format(Strings.Sites_DownloadingFrankenphpForPhp, site.Php, Math.Round(state.Progress * 100))
        : Localization.Format(Strings.Sites_DownloadingFrankenphp, site.Php);
    public double Progress => State is { HasProgress: true } state ? state.Progress : 0;
    public bool IsProgressIndeterminate => State is not { HasProgress: true };
    public bool IsWaiting => State is { IsRunning: false, IsInstalling: false };
    public string Problem => IsWaiting ? State!.Problem : "";

    public bool IsRestarting => restarting;
    public bool CanRestart => site.FrankenPhp is { IsRunning: true } && !restarting;

    public string Note => franken && !site.OctaneAvailable
        ? Strings.Sites_FrankenphpFootnoteOctane
        : Strings.Sites_FrankenphpFootnote;

    public void Update(Status.Site value, SiteRowViewModel? draft, bool isRestarting)
    {
        site = value;
        row = draft;
        restarting = isRestarting;
        franken = (draft?.Server ?? value.Server) == Status.SiteServer.FrankenPhp;
        octane = (draft?.Worker ?? value.Worker) == Status.SiteServer.Octane;
        serverShown = franken ? 1 : 0;
        modeShown = octane ? 1 : 0;
        Refresh();
    }

    /// <summary>FrankenPHP runs PHP 8.2 and newer; an unknown version doesn't block it (the daemon checks too).</summary>
    internal static bool SupportsFrankenPhp(string php)
    {
        var parts = php.Split('.');
        if (parts.Length < 2 || !int.TryParse(parts[0], out var major) || !int.TryParse(parts[1], out var minor))
        {
            return true;
        }
        return major > 8 || major == 8 && minor >= 2;
    }

    [RelayCommand]
    private void Restart()
    {
        var name = site.Name;
        host.Start(name, () => host.RestartAsync(name));
    }

    [RelayCommand]
    private void ShowLog() => host.OpenLog(site.FrankenPhp is { Log.Length: > 0 } f ? f.Log : $"frankenphp-{site.Name}");
}

/// <summary>
///     A PHP site's Laravel Reverb server: turned on here, Flock runs php artisan reverb:start and
///     serves it at wss://&lt;domain&gt;/app. Shows its state, what the app's .env needs, Restart and
///     its output.
/// </summary>
internal sealed partial class ReverbSectionViewModel(ISiteDetailHost host, OutputViewModel output) : DetailViewModel
{
    private Status.Site site = new();
    private Status.SiteReverb? reverb;
    private string address = "";
    private bool restarting;
    private bool shown;

    public OutputViewModel OutputPanel { get; } = output;

    public bool IsOn
    {
        get => shown;
        set
        {
            if (Refreshing || value == shown)
            {
                return;
            }
            // Shown again afterwards, so a refused change snaps back.
            shown = value;
            var name = site.Name;
            host.Start(name, () => host.UpdateSiteAsync(name, new SiteChange(Reverb: value), null));
        }
    }

    public bool CanToggle => site.ReverbAvailable || reverb is not null;
    public bool IsOff => reverb is null;
    public string OffNote => site.ReverbAvailable
        ? Localization.Format(Strings.Sites_Reverb_Explanation, address)
        : Strings.Sites_Reverb_Install;
    public bool HasReverb => reverb is not null;
    public bool IsRunning => reverb?.State == Status.ReverbState.Running;
    public string RunningText => Localization.Format(Strings.Sites_Reverb_RunningAt, address);
    public bool IsFailed => reverb?.State == Status.ReverbState.Failed;
    public string FailedText => reverb is { Problem.Length: > 0 } ? reverb.Problem : Strings.Sites_Server_Failed;
    public bool IsUnavailable => reverb is not null && !IsRunning && !IsFailed;
    public string UnavailableText => reverb is { Problem.Length: > 0 } ? reverb.Problem : Strings.Sites_Reverb_CantRun;
    public bool HasEnv => reverb is { Env.Count: > 0 };
    public string Env => reverb is null ? "" : string.Join("\n", reverb.Env);
    public bool IsRestarting => restarting;
    public bool CanRestart => reverb?.IsRunning == true && !restarting;
    public bool HasOutput => Output is not null;

    /// <summary>Reverb's output, once it has a log.</summary>
    public OutputSource? Output => reverb is { Log.Length: > 0 } ? new OutputSource(reverb.Log, reverb.IsRunning) : null;

    public void Update(Status.Site value, SiteRowViewModel? row, bool isRestarting)
    {
        site = value;
        reverb = value.Reverb;
        address = AddressOf(value, row?.Secured ?? value.Secured);
        restarting = isRestarting;
        shown = reverb is not null;
        Refresh();
    }

    [RelayCommand]
    private void Restart()
    {
        var name = site.Name;
        host.Start(name, () => host.RestartReverbAsync(name));
    }

    /// <summary>
    ///     Where browsers reach the site's Reverb server: the site's address as a WebSocket one,
    ///     wss://app.test/app (with its port when Flock serves another than 443), ws:// without HTTPS.
    ///     While HTTPS is being switched (secured differs from the site's), the address it will have.
    /// </summary>
    internal static string AddressOf(Status.Site site, bool secured)
    {
        if (secured == site.Secured && site.Url is { } url)
        {
            if (url.StartsWith("https://", StringComparison.Ordinal))
            {
                return $"wss://{url["https://".Length..].TrimEnd('/')}/app";
            }
            if (url.StartsWith("http://", StringComparison.Ordinal))
            {
                return $"ws://{url["http://".Length..].TrimEnd('/')}/app";
            }
        }
        return $"{(secured ? "wss" : "ws")}://{site.Domain}/app";
    }

    [RelayCommand]
    private void CopyEnv() => host.Copy(Env);
}

/// <summary>
///     A Node.js site's dev server (started on the site's first visit, asleep after 30 idle minutes):
///     its state, Start, Stop and Restart, the command and its output.
/// </summary>
internal sealed partial class DevServerSectionViewModel(ISiteDetailHost host, OutputViewModel output) : DetailViewModel
{
    private Status.Site site = new();
    private Status.SiteNodeServer? server;

    public OutputViewModel OutputPanel { get; } = output;

    public bool HasServer => server is not null;
    public bool HasNoServer => server is null;
    public string Tone => ToneName(server?.State switch
    {
        Status.NodeServerState.Running => Ui.Tone.Success,
        Status.NodeServerState.Starting => Ui.Tone.Caution,
        Status.NodeServerState.Failed => Ui.Tone.Critical,
        _ => Ui.Tone.Neutral,
    });
    public string StateLabel => server?.StateLabel ?? "";
    /// <summary>What's wrong, unless the state already says it.</summary>
    public string Problem => server is { State: not Status.NodeServerState.Failed } ? server.Problem : "";
    public string Command => server?.Command ?? "";
    public bool CanStart => server?.CanStart == true;
    public bool CanStop => server?.CanStop == true;
    public bool CanRestart => server?.CanRestart == true;

    public OutputSource? Output => server is null ? null
        : new OutputSource(server.Log, server.State is Status.NodeServerState.Starting or Status.NodeServerState.Running);

    public void Update(Status.Site value)
    {
        site = value;
        server = value.IsNode ? value.NodeServer : null;
        Refresh();
    }

    [RelayCommand]
    private void StartServer()
    {
        var name = site.Name;
        host.Start(name, () => host.Sites.RestartAsync(name));
    }

    [RelayCommand]
    private void StopServer()
    {
        var name = site.Name;
        host.Start(name, () => host.Sites.StopSiteAsync(name));
    }

    [RelayCommand]
    private void RestartServer()
    {
        var name = site.Name;
        host.Start(name, () => host.RestartAsync(name));
    }
}

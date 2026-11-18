using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Kit;
using Flock.Tray.Features.Sites;

namespace Flock.Tray.ViewModels;

/// <summary>
///     What the details of a site, worktree or proxy ask of the Sites pane: running an action of a site
///     (one at a time, the details shown again afterwards), its dialogs, and opening things.
/// </summary>
internal interface ISiteDetailHost
{
    SitesService Sites { get; }

    /// <summary>Runs an action of a site or proxy unless one is running; shows the details again afterwards unless refreshAfter is false.</summary>
    void Start(string resource, Func<Task> action, bool refreshAfter = true);

    /// <summary>Changes a site, showing draft (which returns its rollback) until the daemon has it.</summary>
    Task UpdateSiteAsync(string site, SiteChange change, Func<Action?>? draft);

    /// <summary>Runs an action once the current input event is handled, e.g. a picker snapping back.</summary>
    void Post(Action action);

    void Open(Status.Site site);
    void OpenUrl(string url);
    void Reveal(string path);
    void OpenLog(string log);
    void Copy(string text);
    void Select(string name);

    Task RemoveSiteAsync(Status.Site site);
    Task ShowExtensionsAsync(Status.Site site);
    Task PinAsync(Status.Site site, string tool, string version, string details);
    Task UnpinAsync(Status.Site site, string tool, string details);
    Task AddDomainAsync(Status.Site site);
    Task RemoveDomainAsync(Status.Site site, Status.SiteDomain domain);
    Task ChooseRootAsync(Status.Site site);
    Task AddBoostGuidelineAsync(Status.Site site, bool missing);
    Task StartShareAsync(Status.Site site);
    Task CopyQrAsync(string data);
    Task SaveQrAsync(string data, string site);
    Task NewWorktreeAsync(Status.Site site);

    /// <summary>Restarts a site's FrankenPHP or dev server, shown as busy meanwhile.</summary>
    Task RestartAsync(string site);

    /// <summary>Restarts a site's Reverb server, shown as busy meanwhile.</summary>
    Task RestartReverbAsync(string site);

    Task RemoveWorktreeAsync(Status.Site worktree);
    Task SwitchDatabaseAsync(Status.Site worktree, string mode);
    Task SetUpWorktreeAsync(Status.Site worktree, bool again);
    Task ShowSetupLogAsync(Status.Site worktree);

    Task RemoveProxyAsync(Status.Proxy proxy);
    Task ChangeUpstreamAsync(Status.Proxy proxy);
    Task ChangeCommandAsync(Status.Proxy proxy);
}

/// <summary>The output a detail shows: a server's log, read every second while it's live.</summary>
internal sealed record OutputSource(string Log, bool Live);

/// <summary>
///     A part of the details over a status snapshot. A new snapshot makes every binding read again;
///     meanwhile the controls that follow it (a switch, a picker) don't act on what they're given.
/// </summary>
internal abstract class DetailViewModel : ObservableObject
{
    /// <summary>The bindings are reading a new snapshot.</summary>
    protected bool Refreshing { get; private set; }

    /// <summary>Every binding reads again; first the given properties (the lists a picker's selection is in).</summary>
    protected void Refresh(params string[] first)
    {
        Refreshing = true;
        try
        {
            foreach (var property in first)
            {
                OnPropertyChanged(property);
            }
            OnPropertyChanged(string.Empty);
        }
        finally
        {
            Refreshing = false;
        }
    }

    /// <summary>One binding reads again, e.g. a picker snapping back to what the snapshot says.</summary>
    protected void RefreshOne(string property)
    {
        Refreshing = true;
        try
        {
            OnPropertyChanged(property);
        }
        finally
        {
            Refreshing = false;
        }
    }

    /// <summary>The list, or the one shown when it has the same items (so a picker keeps its selection).</summary>
    protected static IReadOnlyList<string> Same(IReadOnlyList<string> shown, List<string> items) =>
        shown.SequenceEqual(items) ? shown : items;

    /// <summary>A server's state as a dot's tone (see ToneBrushConverter).</summary>
    protected static string ToneName(Ui.Tone tone) => tone.ToString();

    /// <summary>"22.11.0" → "22"; pins and isolations are majors.</summary>
    protected static string Major(string version) => version.Split('.')[0];
}

/// <summary>A note that wraps, with an action under it (or none).</summary>
internal sealed partial class HintViewModel(string text, string? action, Action? run) : ObservableObject
{
    public string Text { get; } = text;
    public string ActionText { get; } = action ?? "";
    public bool HasAction => action is not null && run is not null;

    [CommunityToolkit.Mvvm.Input.RelayCommand]
    private void Run() => run?.Invoke();
}

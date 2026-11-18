using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Dispatching;

namespace Flock.Tray;

/// <summary>
/// Posts a Windows notification when a worktree's setup fails and no progress dialog follows it:
/// worktrees an agent app or git created, or whose progress was hidden. It comes from the tray
/// icon, so it needs no app registration or extra runtime, and clicking it shows the worktree in
/// Settings → Sites. Without a tray icon (screenshots, tests) nothing is posted.
/// </summary>
internal sealed class WorktreeNotifier
{
    // Worktrees whose setup a progress dialog shows, by path.
    private readonly HashSet<string> followed = new(StringComparer.OrdinalIgnoreCase);
    private RuntimeService? model;
    private DispatcherQueue? dispatcher;
    private ITrayIcon? tray;
    private Status? previous;
    // The worktree of the last notification: the icon shows one at a time, so a click is about it.
    private string? shown;

    public void Follow(string path) => followed.Add(path);

    public void Unfollow(string path) => followed.Remove(path);

    /// <summary>Watches the status and posts through the tray icon.</summary>
    public void Start(RuntimeService state, DispatcherQueue queue, ITrayIcon icon)
    {
        model = state;
        dispatcher = queue;
        tray = icon;
        previous = state.Status;
        tray.NotificationClicked += Clicked;
        state.Changed += Check;
    }

    /// <summary>Called when the app quits.</summary>
    public void Stop()
    {
        if (model is not null)
        {
            model.Changed -= Check;
        }
        if (tray is not null)
        {
            tray.NotificationClicked -= Clicked;
        }
        model = null;
        tray = null;
    }

    /// <summary>Notifies of the worktrees whose setup failed since the last status.</summary>
    private void Check()
    {
        var status = model?.Status;
        if (status is null || ReferenceEquals(status, previous))
        {
            return;
        }
        var before = previous;
        previous = status;
        if (before is null)
        {
            return;
        }
        foreach (var site in NewlyFailed(before, status, followed))
        {
            Post(site, site.Worktree!);
        }
    }

    /// <summary>
    /// The worktrees whose setup failed since before and that no dialog follows: one that wasn't
    /// failed then, or wasn't there at all (a setup can fail before a status shows it running).
    /// </summary>
    internal static IEnumerable<Status.Site> NewlyFailed(Status before, Status now, IReadOnlySet<string> followed)
    {
        var states = before.Sites.GroupBy(s => s.Name).ToDictionary(g => g.Key, g => g.First().Worktree?.State);
        return now.Sites.Where(site => site.IsWorktree && site.Worktree is { IsFailed: true } && !followed.Contains(site.Path)
            && (!states.TryGetValue(site.Name, out var state) || state != Status.WorktreeState.Failed));
    }

    private void Post(Status.Site worktree, Status.WorktreeSetup setup)
    {
        try
        {
            tray?.ShowNotification(Localization.Format(Strings.Worktrees_SetupFailedTitle, worktree.WorktreeLabel),
                $"{worktree.Parent}: {(setup.Error.Length > 0 ? setup.Error : Strings.Worktrees_SeeLog)}");
            shown = worktree.Name;
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    private void Clicked()
    {
        if (shown is { } name)
        {
            dispatcher?.TryEnqueue(() => App.Current.OpenSite(name));
        }
    }
}

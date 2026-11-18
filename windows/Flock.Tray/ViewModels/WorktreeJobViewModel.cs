using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;

namespace Flock.Tray.ViewModels;

public enum WorktreeJobPhase { Starting, Running, Ready, Failed }

/// <summary>
///     A worktree's job: creating it, setting it up again or switching its database, then its setup
///     step by step with its log. Closing stops following it, not the job.
/// </summary>
public sealed class WorktreeJobViewModel(RuntimeService model, Action<string> follow) : ObservableObject
{
    private readonly CancellationTokenSource lifetime = new();
    private WorktreeJobPhase phase = WorktreeJobPhase.Starting;
    private string step = "";
    private string error = "";
    private string log = "";

    public WorktreeJobPhase Phase => phase;
    public string? Path { get; private set; }
    public string Log => log;

    /// <summary>The worktree, once its setup is done.</summary>
    public Status.Site? Ready => phase == WorktreeJobPhase.Ready ? Worktree : null;

    public string Headline => phase switch
    {
        WorktreeJobPhase.Starting => Strings.Worktrees_Job_Starting,
        WorktreeJobPhase.Running when step.Length > 0 => char.ToUpperInvariant(step[0]) + step[1..] + "…",
        WorktreeJobPhase.Running => Strings.Worktrees_SettingUp,
        WorktreeJobPhase.Ready => Worktree?.Url is { } url ? Localization.Format(Strings.Worktrees_Job_ReadyAt, url) : Strings.Worktrees_Job_Ready,
        _ => error,
    };

    /// <summary>Open in Browser once it's ready, Run Setup Again after a failure.</summary>
    public string PrimaryAction => phase switch
    {
        WorktreeJobPhase.Ready when Worktree?.Url is not null => Strings.Common_OpenInBrowser,
        WorktreeJobPhase.Failed when Path is not null => Strings.Worktrees_RunAgain,
        _ => "",
    };

    public bool IsBusy => phase is WorktreeJobPhase.Starting or WorktreeJobPhase.Running;
    public Visibility BusyVisibility => IsBusy ? Visibility.Visible : Visibility.Collapsed;
    public Visibility ReadyVisibility => phase == WorktreeJobPhase.Ready ? Visibility.Visible : Visibility.Collapsed;
    public Visibility FailedVisibility => phase == WorktreeJobPhase.Failed ? Visibility.Visible : Visibility.Collapsed;

    private Status.Site? Worktree => Path is null ? null
        : model.Status?.Sites.FirstOrDefault(s => s.IsWorktree && string.Equals(s.Path, Path, StringComparison.OrdinalIgnoreCase));

    /// <summary>
    ///     Starts the job (its folder, or why it couldn't start) and follows the setup until it's ready
    ///     or fails, or <see cref="Close" /> is called. Closing doesn't stop the job.
    /// </summary>
    public async Task RunAsync(Func<CancellationToken, Task<(string? Path, string? Error)>> start)
    {
        log = "";
        OnPropertyChanged(nameof(Log));
        Show(WorktreeJobPhase.Starting);
        var (folder, failure) = await start(CancellationToken.None);
        if (failure is not null || folder is null)
        {
            Path ??= folder;
            error = failure ?? Strings.Worktrees_Job_NoPath;
            Show(WorktreeJobPhase.Failed);
            return;
        }
        Path = folder;
        if (lifetime.IsCancellationRequested)
        {
            // Hidden while it started: the notifier tells of a failure.
            return;
        }
        follow(folder);
        step = "";
        Show(WorktreeJobPhase.Running);

        var token = lifetime.Token;
        while (!token.IsCancellationRequested)
        {
            try
            {
                var setup = await model.Client.WorktreeSetupAsync(folder, token);
                if (setup.Log.Length > 0)
                {
                    try
                    {
                        ShowLog(await model.Client.ServiceLogAsync(setup.Log, 400, token));
                    }
                    catch (FlockException)
                    {
                        // Not written yet.
                    }
                }
                if (setup.State == Status.WorktreeState.Ready)
                {
                    // The status gives its address.
                    await model.RefreshAsync();
                    Show(WorktreeJobPhase.Ready);
                    return;
                }
                if (setup.IsFailed)
                {
                    error = Localization.Format(Strings.Worktrees_Job_SetupFailed, setup.Error.Length > 0 ? setup.Error : Strings.Worktrees_Job_SeeLog);
                    Show(WorktreeJobPhase.Failed);
                    return;
                }
                step = setup.Step;
                Show(WorktreeJobPhase.Running);
            }
            catch (OperationCanceledException) when (token.IsCancellationRequested)
            {
                return;
            }
            catch (FlockException)
            {
                // Not known yet (a moment after creating it), or the daemon restarts: ask again.
            }
            try
            {
                await Task.Delay(TimeSpan.FromMilliseconds(500), token);
            }
            catch (OperationCanceledException)
            {
                return;
            }
        }
    }

    /// <summary>Open in Browser or Run Setup Again.</summary>
    public async Task PrimaryAsync()
    {
        if (phase == WorktreeJobPhase.Ready && Worktree is { } worktree)
        {
            model.Open(worktree);
        }
        else if (phase == WorktreeJobPhase.Failed && Path is { } folder)
        {
            var name = Worktree?.Name ?? folder;
            await RunAsync(async token => (folder, await model.Sites.SetupWorktreeAsync(name, folder, again: true, token)));
        }
    }

    public void Close() => lifetime.Cancel();

    private void ShowLog(string text)
    {
        if (text != log)
        {
            log = text;
            OnPropertyChanged(nameof(Log));
        }
    }

    private void Show(WorktreeJobPhase next)
    {
        phase = next;
        OnPropertyChanged(nameof(Phase));
        OnPropertyChanged(nameof(Headline));
        OnPropertyChanged(nameof(PrimaryAction));
        OnPropertyChanged(nameof(BusyVisibility));
        OnPropertyChanged(nameof(ReadyVisibility));
        OnPropertyChanged(nameof(FailedVisibility));
    }
}

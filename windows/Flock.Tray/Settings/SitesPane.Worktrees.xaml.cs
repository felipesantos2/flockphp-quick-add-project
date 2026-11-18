using Flock.Kit;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray.Settings;

/// <summary>
/// Git worktrees: the daemon serves each worktree of a site at &lt;branch&gt;.&lt;site&gt;.test and sets it
/// up (dependencies, .env, installs, hooks, optionally a database of its own). A site lists its
/// worktrees and creates new ones; a worktree shows its branch and folder, its database and its setup
/// (see WorktreeDetailViewModel). These are its dialogs and jobs.
/// </summary>
internal sealed partial class SitesPane
{
    /// <summary>Asks for the new worktree in a dialog, then creates it; a failure shows in the dialog.</summary>
    private async Task NewWorktree(Status.Site site)
    {
        var token = activeLifetime?.Token ?? CancellationToken.None;
        var form = new NewWorktreeView(site);
        var dialog = new ContentDialog
        {
            Title = Localization.Format(Strings.Worktrees_NewTitle, site.Name),
            Content = form,
            PrimaryButtonText = Strings.Worktrees_Create,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot,
        };
        dialog.Resources["ContentDialogMaxWidth"] = 520.0;

        // Create turns the dialog into the job's progress; it stays open until it is closed.
        WorktreeJobView? job = null;
        Task? run = null;
        dialog.PrimaryButtonClick += (_, args) =>
        {
            if (job is not null)
            {
                return;
            }
            args.Cancel = true;
            var (name, start, db) = (form.Branch, form.Start, form.Database);
            job = new WorktreeJobView(dialog, Model, Localization.Format(Strings.Worktrees_NewTitle, site.Name));
            run = job.RunAsync(cancellationToken => Model.Sites.CreateWorktreeAsync(site.Name, name, start, db, cancellationToken));
        };
        var load = form.LoadBranchesAsync(Model.Sites, token);
        await dialog.ShowAsync();
        await load;
        if (run is not null)
        {
            await run;
        }
        if (job?.Ready is { } created)
        {
            Select(created.Name);
        }
    }

    /// <summary>Confirms switching the worktree's database (dropping its own), then switches it as the worktree's job.</summary>
    private async Task SwitchDatabase(Status.Site worktree, string mode)
    {
        var label = worktree.WorktreeLabel;
        var parent = worktree.Parent;
        if (worktree.Worktree is { HasOwnDatabase: true } setup)
        {
            var dropped = mode switch
            {
                Status.WorktreeDatabase.Copy => Strings.Worktrees_DropDetailsCopy,
                Status.WorktreeDatabase.Fresh => Strings.Worktrees_DropDetailsFresh,
                _ => Strings.Worktrees_DropDetailsShared,
            };
            if (!await Window.Confirm(Localization.Format(Strings.Worktrees_DropQuestion, setup.Database), Localization.Format(dropped, label, parent, setup.Database),
                    Strings.Worktrees_Switch, destructive: true))
            {
                return;
            }
        }
        else
        {
            var (question, details) = mode switch
            {
                Status.WorktreeDatabase.Copy => (Strings.Worktrees_GiveCopyQuestion, Strings.Worktrees_GiveCopyDetails),
                Status.WorktreeDatabase.Fresh => (Strings.Worktrees_GiveFreshQuestion, Strings.Worktrees_GiveFreshDetails),
                _ => (Strings.Worktrees_GiveSharedQuestion, Strings.Worktrees_GiveSharedDetails),
            };
            if (!await Window.Confirm(Localization.Format(question, label, parent), details, Strings.Worktrees_Switch))
            {
                return;
            }
        }
        await FollowJob(Localization.Format(Strings.Worktrees_SwitchingDatabase, label), worktree.Path, token => Model.Sites.SwitchWorktreeDatabaseAsync(worktree.Name, worktree.Path, mode, token));
    }

    /// <summary>Sets the worktree up (again: copies what's missing, installs, sets up the database and runs the hooks again).</summary>
    private Task SetUpWorktree(Status.Site worktree, bool again) =>
        FollowJob(Localization.Format(Strings.Worktrees_SettingUpTitle, worktree.WorktreeLabel), worktree.Path, token => Model.Sites.SetupWorktreeAsync(worktree.Name, worktree.Path, again, token));

    /// <summary>The log of the worktree's setup, followed while it runs.</summary>
    private Task ShowSetupLog(Status.Site worktree)
    {
        var name = worktree.Name;
        var log = worktree.Worktree?.Log ?? "";
        return JobLogDialog.ShowAsync(Window, Localization.Format(Strings.Worktrees_SetupLogTitle, worktree.WorktreeLabel),
            token => Model.Client.ServiceLogAsync(log, 400, token),
            () => Model.Status?.Sites.FirstOrDefault(s => s.Name == name)?.Worktree?.IsBusy == true);
    }

    /// <summary>Starts a job of the worktree at path (its error, or null) and follows it in a progress dialog.</summary>
    private Task FollowJob(string title, string path, Func<CancellationToken, Task<string?>> start) =>
        WorktreeJobView.ShowAsync(Window, Model, title, async token => (path, await start(token)));

    /// <summary>
    /// Removes the worktree with git (after its before_delete hook), and its own database; the
    /// branch stays. When git refuses (e.g. uncommitted changes), offers to remove it anyway.
    /// </summary>
    private async Task RemoveWorktree(Status.Site worktree)
    {
        var label = worktree.WorktreeLabel;
        var details = worktree.Worktree is { HasOwnDatabase: true } setup
            ? Localization.Format(Strings.Worktrees_RemoveDetailsDatabase, worktree.Path, setup.Database)
            : Localization.Format(Strings.Worktrees_RemoveDetails, worktree.Path);
        if (worktree.Branch.Length > 0)
        {
            details += " " + Localization.Format(Strings.Worktrees_BranchStays, worktree.Branch);
        }
        if (!await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, label), details, Strings.Common_Remove, destructive: true))
        {
            return;
        }
        var error = await Model.Sites.RemoveWorktreeAsync(worktree.Name, worktree.Path, force: false);
        if (error is not null)
        {
            if (!await Window.Confirm(Localization.Format(Strings.Worktrees_CouldNotRemove, label), error, Strings.Worktrees_RemoveAnyway, destructive: true))
            {
                return;
            }
            error = await Model.Sites.RemoveWorktreeAsync(worktree.Name, worktree.Path, force: true);
            if (error is not null)
            {
                await Ui.Inform(Window.XamlRoot, Localization.Format(Strings.Worktrees_CouldNotRemove, label), error);
                return;
            }
        }
        if (worktree.Parent is { } parent && Model.Status?.Sites.Any(s => s.Name == parent) == true)
        {
            Select(parent);
        }
    }
}

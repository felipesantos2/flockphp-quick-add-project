using System.ComponentModel;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     Follows a worktree's job in a dialog. The dialog never closes by itself: Hide leaves the job
///     running (a failure is then notified), and once it's done it offers Open in Browser, or Run
///     Setup Again after a failure.
/// </summary>
internal sealed partial class WorktreeJobView : UserControl
{
    private readonly ContentDialog dialog;
    private readonly WorktreeJobViewModel viewModel;

    /// <summary>Takes the dialog over (a new one, or the New Worktree dialog once Create is clicked).</summary>
    public WorktreeJobView(ContentDialog dialog, RuntimeService model, string title)
    {
        this.dialog = dialog;
        viewModel = new WorktreeJobViewModel(model, App.Current.WorktreeNotifier.Follow);
        InitializeComponent();
        DataContext = viewModel;
        dialog.Title = title;
        dialog.Content = this;
        dialog.Resources["ContentDialogMaxWidth"] = 660.0;
        dialog.SecondaryButtonText = "";
        dialog.PrimaryButtonClick += PrimaryClicked;
        dialog.Closed += DialogClosed;
        viewModel.PropertyChanged += ViewModelChanged;
        UpdateButtons();
    }

    /// <summary>The worktree, once its setup is done (Close then shows it).</summary>
    public Status.Site? Ready => viewModel.Ready;

    public Task RunAsync(Func<CancellationToken, Task<(string? Path, string? Error)>> start) => viewModel.RunAsync(start);

    /// <summary>Shows a new dialog following a job, until it is closed; the worktree when it got ready.</summary>
    public static async Task<Status.Site?> ShowAsync(SettingsWindow window, RuntimeService model, string title,
        Func<CancellationToken, Task<(string? Path, string? Error)>> start)
    {
        var dialog = new ContentDialog { XamlRoot = window.XamlRoot };
        var view = new WorktreeJobView(dialog, model, title);
        var run = view.RunAsync(start);
        await dialog.ShowAsync();
        await Observe(run);
        return view.Ready;
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (args.PropertyName == nameof(WorktreeJobViewModel.Log))
        {
            LogScroller.UpdateLayout();
            LogScroller.ChangeView(null, LogScroller.ScrollableHeight, null, disableAnimation: true);
        }
        else if (args.PropertyName == nameof(WorktreeJobViewModel.PrimaryAction))
        {
            UpdateButtons();
        }
    }

    private void UpdateButtons()
    {
        dialog.PrimaryButtonText = viewModel.PrimaryAction;
        dialog.CloseButtonText = viewModel.IsBusy ? Strings.Common_Hide : Strings.Common_Close;
        dialog.DefaultButton = viewModel.IsBusy ? ContentDialogButton.None : ContentDialogButton.Close;
        dialog.IsPrimaryButtonEnabled = true;
    }

    // Open in Browser and Run Setup Again keep the dialog open.
    private void PrimaryClicked(ContentDialog sender, ContentDialogButtonClickEventArgs args)
    {
        args.Cancel = true;
        _ = Observe(viewModel.PrimaryAsync());
    }

    private void DialogClosed(ContentDialog sender, ContentDialogClosedEventArgs args)
    {
        viewModel.PropertyChanged -= ViewModelChanged;
        viewModel.Close();
        if (viewModel.Path is { } path)
        {
            App.Current.WorktreeNotifier.Unfollow(path);
        }
    }

    private static async Task Observe(Task task)
    {
        try
        {
            await task;
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }
}

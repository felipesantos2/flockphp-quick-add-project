using Flock.Kit;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray;

/// <summary>One shared dialog surface for PHP, FrankenPHP, and catalog job logs.</summary>
internal static class JobLogDialog
{
    public static async Task ShowAsync(FlockWindow window, string title, Func<CancellationToken, Task<string>> load, Func<bool> active, int tailLines = 0)
    {
        var viewModel = new JobLogViewModel(load, active, tailLines);
        var text = new TextBlock { FontFamily = Ui.Mono, FontSize = 12, IsTextSelectionEnabled = true };
        var scroller = new ScrollViewer
        {
            Content = text,
            Width = 680,
            Height = 380,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        var dialog = new ContentDialog
        {
            Title = title,
            Content = scroller,
            PrimaryButtonText = Strings.Common_Copy,
            CloseButtonText = Strings.Common_Done,
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = window.Content.XamlRoot,
        };
        dialog.Resources["ContentDialogMaxWidth"] = 760.0;
        dialog.IsPrimaryButtonEnabled = false;
        viewModel.Changed += Apply;
        dialog.PrimaryButtonClick += (_, args) =>
        {
            args.Cancel = true;
            if (viewModel.CanCopy)
            {
                Ui.Copy(viewModel.Text);
            }
        };

        void Apply()
        {
            if (window.DispatcherQueue.HasThreadAccess)
            {
                ApplyOnUi();
            }
            else
            {
                window.DispatcherQueue.TryEnqueue(ApplyOnUi);
            }
        }
        void ApplyOnUi()
        {
            text.Text = viewModel.Error is { } error ? Localization.Format(Strings.Worktrees_Job_CouldNotLoad, error) : viewModel.DisplayText;
            dialog.IsPrimaryButtonEnabled = viewModel.CanCopy;
            if (viewModel.State is JobLogState.Loaded)
            {
                scroller.UpdateLayout();
                scroller.ChangeView(null, scroller.ScrollableHeight, null, disableAnimation: true);
            }
        }

        ApplyOnUi();
        var polling = viewModel.StartAsync();
        try
        {
            await dialog.ShowAsync();
        }
        finally
        {
            await viewModel.CloseAsync();
            await polling;
            viewModel.Changed -= Apply;
        }
    }
}
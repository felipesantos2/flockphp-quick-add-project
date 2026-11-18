using System.ComponentModel;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     The output panel of a site's dev server or Reverb, or a proxy's command (its DataContext is the
///     pane's <see cref="OutputViewModel" />). It follows the output while it's scrolled to the end,
///     like a terminal.
/// </summary>
internal sealed partial class OutputPanel : UserControl
{
    private OutputViewModel? viewModel;
    private bool atEnd = true;

    public OutputPanel()
    {
        InitializeComponent();
        DataContextChanged += (_, _) => Follow(DataContext as OutputViewModel);
        Unloaded += (_, _) => Follow(null);
        Loaded += (_, _) => Follow(DataContext as OutputViewModel);
    }

    private void Follow(OutputViewModel? next)
    {
        if (ReferenceEquals(viewModel, next))
        {
            return;
        }
        if (viewModel is not null)
        {
            viewModel.PropertyChanged -= ViewModelChanged;
        }
        viewModel = next;
        if (viewModel is not null)
        {
            viewModel.PropertyChanged += ViewModelChanged;
        }
    }

    private void ScrollerViewChanged(object? sender, ScrollViewerViewChangedEventArgs args) =>
        atEnd = Scroller.VerticalOffset >= Scroller.ScrollableHeight - 4;

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (args.PropertyName != nameof(OutputViewModel.Text) || !atEnd || Visibility != Visibility.Visible)
        {
            return;
        }
        // Once the binding has shown the new text.
        DispatcherQueue.TryEnqueue(() =>
        {
            Scroller.UpdateLayout();
            Scroller.ChangeView(null, Scroller.ScrollableHeight, null, true);
        });
    }
}

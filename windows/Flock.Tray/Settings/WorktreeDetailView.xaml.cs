using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>The selected worktree's details in Settings → Sites (its DataContext is a WorktreeDetailViewModel).</summary>
internal sealed partial class WorktreeDetailView : UserControl
{
    public WorktreeDetailView()
    {
        InitializeComponent();
    }
}

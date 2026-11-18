using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>A Node.js site's or worktree's dev server in its details (its DataContext is a DevServerSectionViewModel).</summary>
internal sealed partial class DevServerSection : UserControl
{
    public DevServerSection()
    {
        InitializeComponent();
    }
}

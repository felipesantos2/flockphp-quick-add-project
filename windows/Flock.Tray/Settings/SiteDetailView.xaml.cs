using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>The selected site's details in Settings → Sites (its DataContext is a SiteDetailViewModel).</summary>
internal sealed partial class SiteDetailView : UserControl
{
    public SiteDetailView()
    {
        InitializeComponent();
    }
}

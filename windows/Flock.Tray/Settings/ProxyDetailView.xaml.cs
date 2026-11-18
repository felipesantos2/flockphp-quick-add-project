using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>The selected proxy's details in Settings → Sites (its DataContext is a ProxyDetailViewModel).</summary>
internal sealed partial class ProxyDetailView : UserControl
{
    public ProxyDetailView()
    {
        InitializeComponent();
    }
}

using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>The PHP extensions dialog's content: a site's extensions, and Add to composer.json.</summary>
internal sealed partial class SiteExtensionsView : UserControl
{
    public SiteExtensionsView(SiteExtensionsViewModel viewModel)
    {
        InitializeComponent();
        DataContext = viewModel;
    }
}

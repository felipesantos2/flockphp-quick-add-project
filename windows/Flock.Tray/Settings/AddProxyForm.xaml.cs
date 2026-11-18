using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>The Add Proxy dialog's form: a name and an upstream, and optionally the commands that start its server.</summary>
internal sealed partial class AddProxyForm : UserControl
{
    public AddProxyForm(XamlRoot root, string tld, Func<Task<string?>> pickFolder)
    {
        InitializeComponent();
        NameBox.PlaceholderText = $"my-app → my-app.{tld}";
        Commands.Show(null, pickFolder);
        Ui.FitDialog(root, Scroller);
    }

    public string ProxyName => NameBox.Text.Trim();
    public string Upstream => UpstreamBox.Text.Trim();
    public ProxyCommandsForm CommandFields => Commands;
}

using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>The Share dialog's form: who may open the shared site, and whether visitors see the app's debug mode.</summary>
internal sealed partial class ShareForm : UserControl
{
    public ShareForm()
    {
        InitializeComponent();
    }

    /// <summary>The email addresses (or *@domain) let in; none: anyone with the link.</summary>
    public string[] Allow => AllowBox.Text.Split([',', ' ', ';', '\t', '\r', '\n'], StringSplitOptions.RemoveEmptyEntries);

    public bool Debug => DebugBox.IsChecked == true;
}

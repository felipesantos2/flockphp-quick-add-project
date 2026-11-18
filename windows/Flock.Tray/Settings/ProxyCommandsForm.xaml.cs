using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>The Start, Stop and Folder fields of a proxy's commands, in the Add Proxy dialog and in their own.</summary>
internal sealed partial class ProxyCommandsForm : UserControl
{
    private Func<Task<string?>>? pickFolder;

    public ProxyCommandsForm()
    {
        InitializeComponent();
    }

    /// <summary>What was entered; without a start command, none (an empty command removes them).</summary>
    public ProxyChange Change => StartBox.Text.Trim() is { Length: > 0 } start
        ? new ProxyChange(Command: start, Dir: FolderBox.Text.Trim(), StopCommand: StopBox.Text.Trim())
        : new ProxyChange(Command: "");

    /// <summary>Fills the fields with a proxy's commands (none: empty, in the home folder); Choose… asks with pick.</summary>
    public void Show(Status.ProxyCommand? current, Func<Task<string?>> pick)
    {
        pickFolder = pick;
        StartBox.Text = current?.Command ?? "";
        StopBox.Text = current?.StopCommand ?? "";
        FolderBox.Text = current?.Dir is { Length: > 0 } dir ? dir : Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
    }

    /// <summary>In a dialog of their own, the fields scroll in the smallest window.</summary>
    public void FitTo(XamlRoot root) => Ui.FitDialog(root, Scroller);

    private async void ChooseClicked(object sender, RoutedEventArgs args)
    {
        try
        {
            if (pickFolder is not null && await pickFolder() is { } picked)
            {
                FolderBox.Text = picked;
            }
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }
}

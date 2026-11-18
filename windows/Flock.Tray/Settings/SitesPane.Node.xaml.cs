using Flock.Kit;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>A proxy's commands: what Flock starts its server with, asked for in a dialog.</summary>
internal sealed partial class SitesPane
{
    /// <summary>Asks for a proxy's commands: Start, Stop and Folder. An empty start command removes them.</summary>
    private async Task ChangeCommandAsync(Status.Proxy proxy)
    {
        var current = proxy.Command;
        var fields = new ProxyCommandsForm();
        fields.Show(current, Window.PickFolder);
        fields.FitTo(Window.XamlRoot);
        var dialog = new ContentDialog
        {
            Title = Localization.Format(current is null ? Strings.Sites_Proxy_AddCommandsTitle : Strings.Sites_Proxy_CommandsTitle, proxy.Name),
            Content = fields,
            PrimaryButtonText = Strings.Common_Save,
            SecondaryButtonText = current is null ? "" : Strings.Sites_Proxy_RemoveCommands,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot,
        };
        var change = await dialog.ShowAsync() switch
        {
            ContentDialogResult.Secondary => new ProxyChange(Command: ""),
            ContentDialogResult.Primary => fields.Change,
            _ => null,
        };
        if (change is null || current is null && change.Command == "")
        {
            return;
        }
        await Model.Sites.UpdateProxyAsync(proxy.Name, change);
    }
}

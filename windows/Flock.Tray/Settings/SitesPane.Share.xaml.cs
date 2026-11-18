using System.Runtime.InteropServices;
using System.Runtime.InteropServices.WindowsRuntime;
using Windows.ApplicationModel.DataTransfer;
using Windows.Storage.Pickers;
using Windows.Storage.Streams;
using Flock.Kit;
using Microsoft.UI.Xaml.Controls;
using WinRT.Interop;
namespace Flock.Tray.Settings;

/// <summary>
///     Sharing a site at a temporary public URL through a Cloudflare quick tunnel (see
///     ShareSectionViewModel): the Share dialog, and copying or saving the share's QR code.
/// </summary>
internal sealed partial class SitesPane
{
    /// <summary>
    ///     Asks who may open the shared site (nobody listed: anyone with the link) and whether visitors see
    ///     the app's debug mode, then shares it.
    /// </summary>
    private async Task StartShare(Status.Site site)
    {
        var form = new ShareForm();
        var dialog = new ContentDialog
        {
            Title = Localization.Format(Strings.Sites_Share_Title, site.Name),
            Content = form,
            PrimaryButtonText = Strings.Sites_Share_Share,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        await Model.Sites.StartShareAsync(site.Name, form.Allow, form.Debug);
    }

    internal static string QrFileName(string site)
    {
        return $"{site}-share-qr.png";
    }

    /// <summary>Copies the share's QR code to the clipboard as an image.</summary>
    private static async Task CopyQr(string data)
    {
        try
        {
            // The clipboard reads the stream when an app pastes, so it stays open.
            var stream = new InMemoryRandomAccessStream();
            await stream.WriteAsync(Convert.FromBase64String(data).AsBuffer());
            stream.Seek(0);
            var package = new DataPackage();
            package.SetBitmap(RandomAccessStreamReference.CreateFromStream(stream));
            Clipboard.SetContent(package);
        }
        catch (Exception e) when (e is FormatException or COMException)
        {
            App.Log(e);
        }
    }

    /// <summary>Asks where to save the share's QR code, then writes the PNG there.</summary>
    private async Task SaveQr(string data, string site)
    {
        var picker = new FileSavePicker { SuggestedFileName = Path.GetFileNameWithoutExtension(QrFileName(site)) };
        picker.FileTypeChoices.Add(Strings.Sites_Share_PngImage, [".png"]);
        InitializeWithWindow.Initialize(picker, Window.Hwnd);
        var file = await picker.PickSaveFileAsync();
        if (file is null)
        {
            return;
        }
        try
        {
            await File.WriteAllBytesAsync(file.Path, Convert.FromBase64String(data));
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or FormatException)
        {
            await Ui.Inform(Window.Content.XamlRoot, Strings.Sites_Share_CouldNotSaveQr, e.Message);
        }
    }
}

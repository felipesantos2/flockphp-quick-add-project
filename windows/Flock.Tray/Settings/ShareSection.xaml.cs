using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.WindowsRuntime;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media.Imaging;
using Windows.ApplicationModel.DataTransfer;
using Windows.Storage;
using Windows.Storage.Streams;
namespace Flock.Tray.Settings;

/// <summary>
///     The Share section of a site's details (its DataContext is a <see cref="ShareSectionViewModel" />).
///     It decodes the share's QR code for its image, and lets it be dragged out as a .png file.
/// </summary>
internal sealed partial class ShareSection : UserControl
{
    private ShareSectionViewModel? viewModel;

    // The QR code decoded last, so the status polls don't decode it again.
    private string shown = "";

    public ShareSection()
    {
        InitializeComponent();
        DataContextChanged += (_, _) => Follow(DataContext as ShareSectionViewModel);
    }

    private void Follow(ShareSectionViewModel? next)
    {
        if (ReferenceEquals(viewModel, next))
        {
            return;
        }
        if (viewModel is not null)
        {
            viewModel.PropertyChanged -= ViewModelChanged;
        }
        viewModel = next;
        if (viewModel is not null)
        {
            viewModel.PropertyChanged += ViewModelChanged;
        }
        ShowQr();
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        if (string.IsNullOrEmpty(args.PropertyName) || args.PropertyName == nameof(ShareSectionViewModel.Qr))
        {
            ShowQr();
        }
    }

    private void ShowQr()
    {
        var data = viewModel?.Qr ?? "";
        if (data == shown)
        {
            return;
        }
        shown = data;
        QrImage.Source = null;
        if (data.Length > 0)
        {
            _ = LoadQr(data);
        }
    }

    private async Task LoadQr(string data)
    {
        try
        {
            var bitmap = new BitmapImage();
            using var stream = new InMemoryRandomAccessStream();
            await stream.WriteAsync(Convert.FromBase64String(data).AsBuffer());
            stream.Seek(0);
            await bitmap.SetSourceAsync(stream);
            if (data == shown)
            {
                QrImage.Source = bitmap;
            }
        }
        catch (Exception e) when (e is FormatException or COMException)
        {
            App.Log(e);
        }
    }

    private async void QrDragStarting(UIElement sender, DragStartingEventArgs e)
    {
        if (viewModel is not { HasQr: true } share)
        {
            e.Cancel = true;
            return;
        }
        var deferral = e.GetDeferral();
        try
        {
            var path = Path.Combine(Path.GetTempPath(), SitesPane.QrFileName(share.SiteName));
            await File.WriteAllBytesAsync(path, Convert.FromBase64String(share.Qr));
            e.Data.SetStorageItems([await StorageFile.GetFileFromPathAsync(path)]);
            e.Data.RequestedOperation = DataPackageOperation.Copy;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or FormatException)
        {
            App.Log(ex);
            e.Cancel = true;
        }
        finally
        {
            deferral.Complete();
        }
    }
}

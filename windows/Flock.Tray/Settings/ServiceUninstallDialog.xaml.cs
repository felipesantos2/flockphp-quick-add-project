using System.ComponentModel;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>Confirms uninstalling a service version and, when applicable, deleting its data.</summary>
internal sealed partial class ServiceUninstallDialog : ContentDialog, INotifyPropertyChanged
{
    private bool deleteData;

    public ServiceUninstallDialog(string title, string details, string? deleteDataLabel = null, string? deletionWarning = null)
    {
        Title = title;
        Details = details;
        DeleteDataLabel = deleteDataLabel ?? "";
        DeletionWarning = deletionWarning ?? "";
        HasData = deleteDataLabel is not null;
        InitializeComponent();
        Ui.Destructive(this);
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    public string Details { get; }
    public string DeleteDataLabel { get; }
    public string DeletionWarning { get; }
    public bool HasData { get; }

    public bool DeleteData
    {
        get => deleteData;
        set
        {
            if (deleteData == value)
            {
                return;
            }
            deleteData = value;
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(DeleteData)));
        }
    }

    public async Task<bool> ShowAsync(XamlRoot root)
    {
        XamlRoot = root;
        return await base.ShowAsync() == ContentDialogResult.Primary;
    }
}

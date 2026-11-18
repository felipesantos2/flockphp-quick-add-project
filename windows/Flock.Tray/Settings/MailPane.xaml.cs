using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

internal sealed partial class MailPane : UserControl, ISettingsPane
{
    private readonly MailSettingsViewModel viewModel;

    public MailPane(SettingsWindow window, RuntimeService model)
    {
        InitializeComponent();
        viewModel = new MailSettingsViewModel(model);
        DataContext = viewModel;
    }

    public string Title => Strings.Settings_Pane_Mail;
    public string Subtitle => Strings.Mail_Subtitle;
    public UIElement Root => this;
    public bool ScrollsItself => false;
    public Task ActivateAsync(CancellationToken hidden)
    {
        return Task.CompletedTask;
    }
    public void Hidden() { }
    public Task ReleaseAsync()
    {
        viewModel.Dispose();
        return Task.CompletedTask;
    }

    private void CopyEnvironment(object sender, RoutedEventArgs args)
    {
        Ui.Copy(viewModel.Environment);
    }
    private void OpenMail(object sender, RoutedEventArgs args)
    {
        App.Current.Open(WindowKind.Mail);
    }
}
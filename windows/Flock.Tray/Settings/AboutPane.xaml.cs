using System.ComponentModel;
using System.Reflection;
using System.Runtime.InteropServices;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

internal sealed partial class AboutPane : UserControl, ISettingsPane
{
    private readonly RuntimeService model;
    private readonly string version;
    private readonly AboutViewModel viewModel;

    public AboutPane(SettingsWindow window, RuntimeService model)
    {
        this.model = model;
        InitializeComponent();
        version = Assembly.GetExecutingAssembly().GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion ?? "dev";
        viewModel = new AboutViewModel(model);
        DataContext = viewModel;
        AppVersion.Text = version.Split('+')[0];
    }

    public string Title => Strings.Settings_Pane_About;
    public string Subtitle => Strings.About_Tagline;
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

    private void ShowDataFolder(object sender, RoutedEventArgs args)
    {
        model.Reveal(FlockPaths.Root);
    }
    private void OpenDocumentation(object sender, RoutedEventArgs args)
    {
        model.Open("https://flockphp.com/docs");
    }
    private void OpenDiscord(object sender, RoutedEventArgs args)
    {
        model.Open("https://discord.flockphp.com");
    }
    private void StarOnGitHub(object sender, RoutedEventArgs args)
    {
        model.Open("https://github.com/flock-php/flock");
    }
    private void Sponsor(object sender, RoutedEventArgs args)
    {
        model.Open("https://github.com/sponsors/flock-php");
    }
    private void OpenLicense(object sender, RoutedEventArgs args)
    {
        model.Open("https://github.com/flock-php/flock/blob/main/LICENSE");
    }
    private void ReportIssue(object sender, RoutedEventArgs args)
    {
        model.Open(IssueUrl(version.Split('+')[0]));
    }

    private string IssueUrl(string appVersion)
    {
        var body = $"\n\n---\nApp: {appVersion}\nDaemon: {model.Status?.Version ?? "not running"}\nOS: {RuntimeInformation.OSDescription}\n";
        return "https://github.com/flock-php/flock/issues/new?body=" + Uri.EscapeDataString(body);
    }

    private async void ConfirmUninstallClicked(object sender, RoutedEventArgs args)
    {
        if (Installation.Uninstaller is null)
        {
            model.ShowRuntimeError(Strings.About_NotInstalledWithSetup);
            return;
        }
        var purge = new CheckBox { Content = Strings.About_AlsoDeleteData };
        var dialog = new ContentDialog
        {
            Title = Strings.About_UninstallQuestion,
            Content = Ui.Column(12,
                Ui.Text(Strings.About_UninstallDetails),
                Ui.Text(Localization.Format(Strings.About_UninstallKeeps, FlockPaths.Root)),
                purge),
            PrimaryButtonText = Strings.Common_Uninstall,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = XamlRoot
        };
        if (await dialog.ShowAsync() == ContentDialogResult.Primary && Installation.StartUninstaller(purge.IsChecked == true))
        {
            App.Current.Quit();
        }
    }

    private sealed class AboutViewModel : INotifyPropertyChanged, IDisposable
    {
        private readonly RuntimeService model;
        public AboutViewModel(RuntimeService model)
        {
            this.model = model;
            model.Changed += Changed;
        }

        public string DaemonVersion => model.Status?.Version ?? Strings.About_DaemonNotRunning;
        public string DataFolder => FlockPaths.Root;
        public void Dispose()
        {
            model.Changed -= Changed;
        }
        public event PropertyChangedEventHandler? PropertyChanged;
        private void Changed()
        {
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(DaemonVersion)));
        }
    }
}
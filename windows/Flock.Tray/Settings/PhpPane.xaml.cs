using System.Collections.ObjectModel;
using System.ComponentModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

internal sealed partial class PhpPane : UserControl, ISettingsPane
{
    private readonly FrankenPhpPaneViewModel frankenRows;
    private readonly RuntimeService model;
    private readonly PhpPaneViewModel phpRows;
    private readonly SettingsWindow window;
    private bool active;
    private CancellationToken activeToken;
    private Task pendingConfirmation = Task.CompletedTask;

    public PhpPane(SettingsWindow window, RuntimeService model)
    {
        this.window = window;
        this.model = model;
        phpRows = new PhpPaneViewModel(model.Php);
        frankenRows = new FrankenPhpPaneViewModel(model.FrankenPhp);
        InitializeComponent();
        DataContext = new PhpBindingViewModel(phpRows, frankenRows);
        model.Changed += ModelChanged;
    }

    public string Title => "PHP";
    public string Subtitle => Strings.Php_Subtitle;
    public UIElement Root => this;
    public bool ScrollsItself => false;

    public async Task ActivateAsync(CancellationToken hidden)
    {
        active = true;
        activeToken = hidden;
        while (!hidden.IsCancellationRequested)
        {
            await Task.WhenAll(phpRows.RefreshAsync(hidden), frankenRows.RefreshAsync(hidden));
            ConfirmPendingLink(hidden);
            var active = (phpRows.Current?.HasActiveJob ?? false) || (frankenRows.Current?.HasRunningJob ?? false);
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(active ? 2 : 30), model.TimeProvider, hidden);
            }
            catch (TaskCanceledException)
            {
                return;
            }
        }
    }

    public Task ReleaseAsync()
    {
        active = false;
        model.Changed -= ModelChanged;
        phpRows.Dispose();
        frankenRows.Dispose();
        return Task.CompletedTask;
    }

    public void Hidden()
    {
        active = false;
    }

    private void ModelChanged()
    {
        if (active)
        {
            ConfirmPendingLink(activeToken);
        }
    }

    private void ConfirmPendingLink(CancellationToken token)
    {
        if (token.IsCancellationRequested || !pendingConfirmation.IsCompleted)
        {
            return;
        }
        if (model.PendingInstall is not { Service: InstallLink.Php } link || link.ResolvePhp(phpRows.Current) is not { } request)
        {
            return;
        }
        if (!ReferenceEquals(model.PendingInstall, link))
        {
            return;
        }
        model.PendingInstall = null;
        pendingConfirmation = ConfirmPendingLinkAsync(request, token);
    }

    private async Task ConfirmPendingLinkAsync(InstallLinkResolution request, CancellationToken token)
    {
        try
        {
            var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            if (!window.DispatcherQueue.TryEnqueue(async () =>
            {
                try
                {
                    if (request.Target is not { } target)
                    {
                        if (!token.IsCancellationRequested)
                        {
                            await Ui.Inform(window.XamlRoot, request.Title, request.Message);
                        }
                        completion.TrySetResult();
                        return;
                    }
                    if (token.IsCancellationRequested || !await window.Confirm(request.Title, request.Message, Strings.Common_Install) || token.IsCancellationRequested)
                    {
                        completion.TrySetResult();
                        return;
                    }
                    App.Current.StartAcceptedInstall(cancellation => model.Php.InstallAsync(target.Line, cancellation));
                    completion.TrySetResult();
                }
                catch (OperationCanceledException) when (token.IsCancellationRequested)
                {
                    completion.TrySetCanceled(token);
                }
                catch (Exception error)
                {
                    App.Log(error);
                    completion.TrySetResult();
                }
            }))
            {
                return;
            }
            using var cancellation = token.Register(() => completion.TrySetCanceled(token));
            await completion.Task;
        }
        catch (OperationCanceledException) when (token.IsCancellationRequested)
        {
        }
    }

    private void OpenPhpActions(object sender, RoutedEventArgs args)
    {
        if (sender is Button button && Row(button) is { } row)
        {
            var menu = new MenuFlyout();
            var ini = model.Configuration.Current?.Php.FirstOrDefault(version => version.Version == row.Minor)?.Ini;
            if (ini is { Length: > 0 })
            {
                menu.Items.Add(Item(Strings.Php_OpenIni, () => model.Open(ini)));
                menu.Items.Add(Item(Strings.Php_RevealIni, () => model.Reveal(ini)));
            }
            if (row.HasRunningJob)
            {
                menu.Items.Add(Item(Strings.Versions_ShowLog, () => ShowPhpLog(row)));
                menu.Items.Add(Item(Strings.Common_Cancel, () => _ = row.CancelCommand.ExecuteAsync(null)));
            }
            else if (row.HasFailedJob)
            {
                menu.Items.Add(Item(Strings.Versions_ShowLog, () => ShowPhpLog(row)));
                menu.Items.Add(Item(Strings.Sites_Share_Dismiss, () => _ = row.DismissCommand.ExecuteAsync(null)));
            }
            else if (row.HasJob)
            {
                menu.Items.Add(Item(Strings.Versions_ShowLog, () => ShowPhpLog(row)));
            }
            else
            {
                if (row.CanMakeDefault) menu.Items.Add(Item(Strings.Versions_MakeDefault, () => MakePhpDefault(row)));
                if (row.IsInstalled) menu.Items.Add(Item(Strings.Versions_ShowLastInstallLog, () => ShowPhpLog(row)));
                if (row.CanUninstall && row.HasUpdate) menu.Items.Add(Item(Strings.Common_UninstallEllipsis, () => _ = UninstallPhpAsync(row)));
            }
            button.Flyout = menu;
            menu.ShowAt(button);
        }
    }

    private void MakePhpDefault(PhpVersionViewModel row)
    {
        _ = RunAsync(row.Minor, async () =>
        {
            if (await model.Configuration.SetDefaultPhpAsync(row.Minor) == OperationOutcome.Succeeded)
            {
                await phpRows.RefreshAsync();
            }
        });
    }

    private void UninstallPhp(object sender, RoutedEventArgs args)
    {
        if (Row(sender) is { } row)
        {
            _ = UninstallPhpAsync(row);
        }
    }

    private async Task UninstallPhpAsync(PhpVersionViewModel row)
    {
        if (!await window.Confirm(Localization.Format(Strings.Services_UninstallQuestion, $"PHP {row.Minor}"),
                Localization.Format(Strings.Php_UninstallDetails, row.Minor), Strings.Common_Uninstall))
        {
            return;
        }
        await RunAsync(row.Minor, () => model.Php.UninstallAsync(row.Minor));
    }

    private void ShowPhpLog(PhpVersionViewModel row)
    {
        _ = JobLogDialog.ShowAsync(window, Localization.Format(Strings.Versions_LogTitle, $"PHP {row.Minor}"), token => model.Php.ReadLogAsync(row.Minor, token),
            () => model.Php.Current?.Versions.FirstOrDefault(v => v.Minor == row.Minor)?.Job?.IsRunning == true, 400);
    }

    private void OpenFrankenPhpActions(object sender, RoutedEventArgs args)
    {
        if (sender is Button button && button.DataContext is FrankenPhpVersionViewModel row)
        {
            var menu = new MenuFlyout();
            if (row.HasRunningJob)
            {
                menu.Items.Add(Item(Strings.Versions_ShowLog, () => ShowFrankenLog(row)));
                menu.Items.Add(Item(Strings.Common_Cancel, () => _ = row.CancelCommand.ExecuteAsync(null)));
            }
            else if (row.HasFailedJob)
            {
                menu.Items.Add(Item(Strings.Versions_ShowLog, () => ShowFrankenLog(row)));
                menu.Items.Add(Item(Strings.Sites_Share_Dismiss, () => _ = row.DismissCommand.ExecuteAsync(null)));
            }
            else
            {
                menu.Items.Add(Item(Strings.Versions_ShowLog, () => ShowFrankenLog(row)));
                if (row.CanUninstall && row.HasUpdate) menu.Items.Add(Item(Strings.Common_UninstallEllipsis, () => _ = UninstallFrankenPhpAsync(row)));
            }
            button.Flyout = menu;
            menu.ShowAt(button);
        }
    }

    private void UninstallFrankenPhp(object sender, RoutedEventArgs args)
    {
        if ((sender as FrameworkElement)?.DataContext is FrankenPhpVersionViewModel row)
        {
            _ = UninstallFrankenPhpAsync(row);
        }
    }

    private async Task UninstallFrankenPhpAsync(FrankenPhpVersionViewModel row)
    {
        if (!await window.Confirm(Localization.Format(Strings.Php_UninstallFrankenQuestion, row.Php), Strings.Php_UninstallFrankenDetails, Strings.Common_Uninstall))
        {
            return;
        }
        await row.UninstallCommand.ExecuteAsync(null);
    }

    private void ShowFrankenLog(FrankenPhpVersionViewModel row)
    {
        _ = JobLogDialog.ShowAsync(window, Localization.Format(Strings.Versions_LogTitle, $"FrankenPHP {row.Php}"), token => model.FrankenPhp.ReadLogAsync(row.Php, token),
            () => model.FrankenPhp.Current?.Builds.FirstOrDefault(v => v.Php == row.Php)?.Job?.IsRunning == true);
    }

    private static MenuFlyoutItem Item(string text, Action action)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => action();
        return item;
    }

    private PhpVersionViewModel? Row(object sender)
    {
        return (sender as FrameworkElement)?.DataContext as PhpVersionViewModel;
    }

    private async Task RunAsync(string key, Func<Task> action)
    {
        try
        {
            await action();
        }
        catch (OperationCanceledException)
        {
        }
    }

    private sealed class PhpBindingViewModel : ObservableObject
    {
        private readonly FrankenPhpPaneViewModel franken;
        private readonly PhpPaneViewModel php;

        public PhpBindingViewModel(PhpPaneViewModel php, FrankenPhpPaneViewModel franken)
        {
            this.php = php;
            this.franken = franken;
            php.PropertyChanged += Changed;
            franken.PropertyChanged += Changed;
        }

        public ObservableCollection<PhpVersionViewModel> Versions => php.Versions;
        public ObservableCollection<FrankenPhpVersionViewModel> Builds => franken.Builds;
        public PhpCatalog? Current => php.Current;
        public FrankenPhpState? FrankenCurrent => franken.Current;
        public bool IsChecking => php.IsChecking;
        public string CheckedText => php.CheckedText;
        public string CheckErrorText => php.CheckErrorText;
        public IAsyncRelayCommand CheckCommand => php.CheckCommand;
        public bool IsFrankenChecking => franken.IsChecking;
        public string FrankenCheckedText => franken.CheckedText;
        public string FrankenCheckErrorText => franken.CheckErrorText;
        public IAsyncRelayCommand FrankenCheckCommand => franken.CheckCommand;
        public Visibility FrankenPhpVisibility => franken.Current?.Available == true ? Visibility.Visible : Visibility.Collapsed;

        private void Changed(object? sender, PropertyChangedEventArgs args)
        {
            if (ReferenceEquals(sender, franken))
            {
                OnPropertyChanged(nameof(Builds));
                OnPropertyChanged(nameof(FrankenCurrent));
                OnPropertyChanged(nameof(IsFrankenChecking));
                OnPropertyChanged(nameof(FrankenCheckedText));
                OnPropertyChanged(nameof(FrankenCheckErrorText));
                OnPropertyChanged(nameof(FrankenPhpVisibility));
                return;
            }

            OnPropertyChanged(args.PropertyName);
        }
    }
}
using System.ComponentModel;
using Flock.Kit;
using Flock.Tray.ViewModels;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

/// <summary>Laravel and Flock service logs with search, filtering, and polling.</summary>
internal sealed partial class LogsWindow : FlockWindow
{
    private readonly LogsViewModel viewModel;
    private (LogsViewModel.LogContent Data, string Query, LogLevelFilter Level)? rendered;

    public LogsWindow(LogsViewModel viewModel) : base(Strings.Menu_Logs, 1000, 660)
    {
        this.viewModel = viewModel;
        InitializeComponent();
        InitializeChrome(WindowRoot, WindowTitleBar, WindowCaption, WindowIcon);
        WindowBody.DataContext = viewModel;
        foreach (var (_, name) in LogFilter.Choices) Levels.Items.Add(name);
        Ui.AutomationName(Levels, Strings.Logs_Level);
        Ui.SettingsShortcut(WindowRoot);
        viewModel.PropertyChanged += ViewModelChanged;
        AppWindow.Changed += WindowChanged;
        viewModel.SetWindowActive(IsWindowActive());
        Render();
        _ = viewModel.RefreshCommand.ExecuteAsync(null);
    }

    public Task ShowSourceAsync(LogsViewModel.Source source)
    {
        return viewModel.ShowSourceAsync(source);
    }

    public override async Task ReleaseAsync()
    {
        AppWindow.Changed -= WindowChanged;
        viewModel.PropertyChanged -= ViewModelChanged;
        viewModel.SetWindowActive(false);
        await viewModel.DisposeAsync();
    }

    private void SearchChanged(AutoSuggestBox sender, AutoSuggestBoxTextChangedEventArgs args)
    {
        viewModel.Query = Search.Text ?? "";
    }

    private void LevelChanged(object sender, SelectionChangedEventArgs args)
    {
        if (Levels.SelectedIndex >= 0) viewModel.Level = LogFilter.Choices[Levels.SelectedIndex].Filter;
    }

    private void AutoRefreshClicked(object sender, RoutedEventArgs args)
    {
        viewModel.AutoRefreshEnabled = AutoRefresh.IsChecked == true;
    }

    private async void SourceClicked(object sender, RoutedEventArgs args)
    {
        if (sender is Button { DataContext: LogsViewModel.Source source }) await viewModel.ShowSourceAsync(source);
    }

    private async void ClearClicked(object sender, RoutedEventArgs args)
    {
        await ObserveClearAsync(async () =>
        {
            if (viewModel.Selection is LogsViewModel.SiteSource site && await Confirm(Localization.Format(Strings.Logs_ClearQuestion, site.Name),
                    Localization.Format(Strings.Logs_ClearDetails, site.Name), Strings.Logs_Clear, true))
                await viewModel.ClearCommand.ExecuteAsync(null);
        }, App.Log);
    }

    internal static async Task ObserveClearAsync(Func<Task> clear, Action<Exception> log)
    {
        try
        {
            await clear();
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception error)
        {
            log(error);
        }
    }

    private void DismissError(InfoBar sender, object args)
    {
        viewModel.DismissError();
    }

    private void WindowChanged(AppWindow sender, AppWindowChangedEventArgs args)
    {
        if (args.DidVisibilityChange || args.DidPresenterChange) viewModel.SetWindowActive(IsWindowActive());
    }

    private bool IsWindowActive()
    {
        return AppWindow.IsVisible && AppWindow.Presenter is not OverlappedPresenter
        {
            State: OverlappedPresenterState.Minimized
        };
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        Render();
    }

    private void Render()
    {
        Ui.ShowError(ErrorBar, viewModel.Error);
        ClearButton.Visibility = viewModel.Selection is LogsViewModel.SiteSource ? Visibility.Visible : Visibility.Collapsed;
        ClearButton.IsEnabled = viewModel.CanClear;
        Levels.IsEnabled = viewModel.Selection is LogsViewModel.SiteSource;
        AutoRefresh.IsChecked = viewModel.AutoRefreshEnabled;
        SiteList.SelectedItem = viewModel.Selection is LogsViewModel.SiteSource selectedSite ? selectedSite : null;
        ServiceList.SelectedItem = viewModel.Selection is LogsViewModel.ServiceSource selectedService ? selectedService : null;
        if (viewModel.Selection is null)
        {
            DetailStateHost.Content = Ui.EmptyState(Ui.GlyphDocument, Strings.Logs_PickLog);
            EntryList.Visibility = Visibility.Collapsed;
            return;
        }
        if (viewModel.Current is not { } current || current.Source != viewModel.Selection)
        {
            rendered = null;
            EntryList.Visibility = Visibility.Collapsed;
            DetailStateHost.Content = viewModel.LoadError is { } failure
                ? Ui.EmptyState(Ui.GlyphWarning, Strings.Logs_CouldNotLoad, failure)
                : new ProgressRing { Width = 32, Height = 32 };
            return;
        }
        var key = (current, viewModel.Query, viewModel.Level);
        if (rendered is { } shown && shown.Equals(key)) return;
        rendered = key;
        switch (current)
        {
            case LogsViewModel.SiteContent site:
                if (site.Entries.Count == 0)
                {
                    EntryList.Visibility = Visibility.Collapsed;
                    DetailStateHost.Content = Ui.EmptyState(Ui.GlyphDocument, Strings.Logs_NoEntries, Localization.Format(Strings.Logs_NoEntriesDetails, site.Site.Name));
                }
                else if (viewModel.FilteredEntries.Count == 0)
                {
                    EntryList.Visibility = Visibility.Collapsed;
                    DetailStateHost.Content = Ui.EmptyState(Ui.GlyphSearch, Strings.Logs_NoMatching, Strings.Logs_NoMatchingDetails);
                }
                else
                {
                    DetailStateHost.Content = null;
                    EntryList.Visibility = Visibility.Visible;
                }
                break;
            case LogsViewModel.ServiceContent service:
                EntryList.Visibility = Visibility.Collapsed;
                var text = MailDetailView.Monospaced(service.Text.Length == 0 ? Strings.Common_EmptyParenthesized :
                    viewModel.FilteredText.Length == 0 ? Strings.Logs_NoMatchingLines : viewModel.FilteredText);
                DetailStateHost.Content = text;
                text.Loaded += (_, _) => text.ChangeView(null, text.ScrollableHeight, null, true);
                break;
        }
    }
}

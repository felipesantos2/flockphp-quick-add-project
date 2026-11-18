using System.ComponentModel;
using System.Text.Json;
using Flock.Kit;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray;

/// <summary>A terminal-like timeline of dumps, queries, logs, views, and jobs.</summary>
internal sealed partial class DumpsWindow : FlockWindow
{
    private readonly ToggleMenuFlyoutItem dumpsInPage = new() { Text = Strings.Dumps_ShowInPageToo };
    private readonly MenuFlyout editors = new();
    private readonly Task initialRefresh;
    private readonly Dictionary<string, ToggleMenuFlyoutItem> kindItems = [];
    private readonly HtmlView timeline;
    private readonly MenuFlyoutItem unsupported = new() { IsEnabled = false, Visibility = Visibility.Collapsed };
    private readonly DumpsViewModel viewModel;
    private bool released;
    private Task timelineLoad = Task.CompletedTask;

    public DumpsWindow(DumpsViewModel viewModel) : base(Strings.Settings_Pane_Dumps, 920, 620)
    {
        this.viewModel = viewModel;
        InitializeComponent();
        InitializeChrome(WindowRoot, WindowTitleBar, WindowCaption, WindowIcon);
        WindowBody.DataContext = viewModel;
        timeline = new HtmlView(true, true, false, viewModel.Model.Shell);
        CaptureKinds.Flyout = BuildCaptureMenu();
        EditorButton.Flyout = BuildEditorMenu();
        ToolTipService.SetToolTip(Capture, Strings.Dumps_CaptureTooltip);
        ToolTipService.SetToolTip(LatestOnly, Strings.Dumps_LatestOnlyTooltip);
        ToolTipService.SetToolTip(EditorButton, Strings.Dumps_EditorTooltip);
        ToolTipService.SetToolTip(CaptureKinds, Strings.Dumps_CaptureKindsTooltip);
        viewModel.PropertyChanged += ViewModelChanged;
        viewModel.AcquireVisibility();
        Closed += (_, _) => Unsubscribe();
        Render(reloadTimeline: true);
        initialRefresh = viewModel.RefreshAsync();
        Ui.SettingsShortcut(WindowRoot);
    }

    private MenuFlyout BuildCaptureMenu()
    {
        var menu = new MenuFlyout();
        foreach (var kind in EventKind.All)
        {
            var item = new ToggleMenuFlyoutItem { Text = EventKind.Name(kind) };
            item.Click += async (_, _) => await viewModel.SetCaptureAsync(kind, item.IsChecked);
            kindItems[kind] = item;
            menu.Items.Add(item);
        }
        menu.Items.Add(new MenuFlyoutSeparator());
        dumpsInPage.Click += async (_, _) => await viewModel.SetDumpsInPageAsync(dumpsInPage.IsChecked);
        menu.Items.Add(dumpsInPage);
        menu.Items.Add(unsupported);
        return menu;
    }

    private MenuFlyout BuildEditorMenu()
    {
        foreach (var editor in EditorInfo.All)
        {
            var item = new RadioMenuFlyoutItem { Text = editor.Name(), GroupName = "editor", IsChecked = EditorInfo.Parse(viewModel.Editor) == editor };
            item.Click += async (_, _) =>
            {
                try { await viewModel.SetEditorAsync(editor); }
                catch (OperationCanceledException) { }
                catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException) { }
            };
            editors.Items.Add(item);
        }
        return editors;
    }

    private void SearchChanged(AutoSuggestBox sender, AutoSuggestBoxTextChangedEventArgs args)
    {
        viewModel.Query = Search.Text ?? "";
    }

    private async void CaptureClicked(object sender, RoutedEventArgs args)
    {
        if (Capture.IsChecked is { } enabled) await viewModel.SetDumpsAsync(enabled);
    }

    private async void LatestOnlyClicked(object sender, RoutedEventArgs args)
    {
        try { await viewModel.SetLatestOnlyAsync(LatestOnly.IsChecked == true); }
        catch (OperationCanceledException) { }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException) { }
    }

    private async void ClearClicked(object sender, RoutedEventArgs args)
    {
        if (await Confirm(Strings.Dumps_ClearQuestion, Strings.Dumps_ClearDetails, Strings.Logs_Clear, true))
        {
            await viewModel.ClearCommand.ExecuteAsync(null);
        }
    }

    private void DismissError(InfoBar sender, object args)
    {
        viewModel.DismissError();
    }

    private void ViewModelChanged(object? sender, PropertyChangedEventArgs args)
    {
        Render(RequiresTimelineReload(args.PropertyName));
    }

    private static bool RequiresTimelineReload(string? propertyName)
    {
        return propertyName is nameof(DumpsViewModel.VisibleDumps) or nameof(DumpsViewModel.VisibleEvents) or nameof(DumpsViewModel.Header)
            or nameof(DumpsViewModel.Editor);
    }

    private void Render(bool reloadTimeline)
    {
        var capturing = viewModel.Capturing;
        Capture.IsChecked = capturing;
        Capture.Label = capturing ? Strings.Dumps_Capturing : Strings.Dumps_CaptureDumps;
        Capture.IsEnabled = viewModel.Status is not null && !viewModel.IsBusy;
        LatestOnly.IsChecked = viewModel.LatestOnly;
        ClearButton.IsEnabled = viewModel.CanClear;
        Ui.ShowError(ErrorBar, viewModel.Error);
        foreach (var (kind, item) in kindItems)
        {
            item.IsChecked = !(viewModel.Status?.CaptureOff.Contains(kind) ?? false);
            item.IsEnabled = capturing && !viewModel.IsBusy;
        }
        dumpsInPage.IsChecked = viewModel.Status?.DumpsInPage == true;
        dumpsInPage.IsEnabled = capturing && !viewModel.IsBusy;
        var editor = EditorInfo.Parse(viewModel.Editor);
        foreach (var item in editors.Items.OfType<RadioMenuFlyoutItem>())
        {
            item.IsChecked = item.Text == editor.Name();
        }
        var missing = viewModel.Status?.CaptureUnsupported ?? [];
        unsupported.Text = missing.Count > 0 ? Localization.Format(Strings.Dumps_CaptureUnavailable, string.Join(", ", missing)) : "";
        unsupported.Visibility = missing.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        var visibleDumps = viewModel.VisibleDumps;
        var visibleEvents = viewModel.VisibleEvents;
        var count = visibleDumps.Count + visibleEvents.Count(e => e.Kind != EventKind.Request);
        Heading = count == 0 ? Strings.Settings_Pane_Dumps : $"{Strings.Settings_Pane_Dumps} ({count})";
        if (count == 0)
        {
            ContentHost.Content = Ui.EmptyState(Ui.GlyphBug, viewModel.Dumps.Count == 0 && viewModel.Events.Count == 0 ? Strings.Dumps_NoneYet : Strings.Dumps_NoneMatching,
                capturing ? Strings.Dumps_EmptyCapturing : Strings.Dumps_EmptyCaptureOff,
                capturing ? null : Ui.AsyncButton(Strings.Dumps_StartCapturing, () => viewModel.SetDumpsAsync(true), true));
            return;
        }
        if (reloadTimeline)
        {
            timelineLoad = timeline.LoadAsync(DumpTimeline.Html(visibleDumps, visibleEvents, viewModel.Header ?? "", EditorInfo.Parse(viewModel.Editor)));
        }
        ContentHost.Content = timeline;
    }

    private void Unsubscribe()
    {
        viewModel.PropertyChanged -= ViewModelChanged;
    }

    public override async Task ReleaseAsync()
    {
        if (released) return;
        released = true;
        Unsubscribe();
        await timelineLoad;
        await timeline.ReleaseAsync();
        await initialRefresh;
        viewModel.Dispose();
    }
}
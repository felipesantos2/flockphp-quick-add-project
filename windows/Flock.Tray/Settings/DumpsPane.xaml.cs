using Flock.Kit;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

internal sealed partial class DumpsPane : UserControl, ISettingsPane
{
    private readonly bool loaded;
    private readonly DumpsViewModel viewModel;

    public DumpsPane(SettingsWindow window, RuntimeService model)
    {
        InitializeComponent();
        viewModel = new DumpsViewModel(model);
        DataContext = viewModel;
        foreach (var editor in EditorInfo.All)
        {
            Editor.Items.Add(editor.Name());
        }
        Editor.SelectedIndex = EditorInfo.All.ToList().IndexOf(EditorInfo.Parse(model.Preferences.Snapshot.Editor));
        loaded = true;
    }

    public string Title => Strings.Settings_Pane_Dumps;
    public string Subtitle => Strings.Dumps_Subtitle;
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

    private void OpenDumps(object sender, RoutedEventArgs args)
    {
        App.Current.Open(WindowKind.Dumps);
    }

    private async void EditorChanged(object sender, SelectionChangedEventArgs args)
    {
        if (!loaded || Editor.SelectedIndex < 0)
        {
            return;
        }
        try
        {
            await viewModel.SetEditorAsync(EditorInfo.All[Editor.SelectedIndex]);
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
        }
    }
}
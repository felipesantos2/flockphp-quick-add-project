using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Data;
namespace Flock.Tray.Settings;

internal sealed partial class AiPane : UserControl, ISettingsPane
{
    private readonly AiViewModel viewModel;

    public AiPane(SettingsWindow window, RuntimeService model)
    {
        InitializeComponent();
        viewModel = new AiViewModel(model.Mcp);
        DataContext = viewModel;
    }

    public string Title => Strings.Settings_Pane_Ai;
    public string Subtitle
        => Strings.Ai_Subtitle;
    public UIElement Root => this;
    public bool ScrollsItself => false;
    public Task ActivateAsync(CancellationToken hidden)
    {
        return viewModel.RefreshAsync(hidden);
    }
    public void Hidden() { }
    public Task ReleaseAsync()
    {
        viewModel.Dispose();
        return Task.CompletedTask;
    }

    private void CopySnippet(object sender, RoutedEventArgs args)
    {
        Ui.Copy(viewModel.Snippet);
    }
}

/// <summary>A state dot's tone ("Success", "Caution", "Critical", "Neutral"; see Ui.StateDot) as its brush.</summary>
internal sealed class ToneBrushConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
    {
        var tone = value is string name && Enum.TryParse<Ui.Tone>(name, out var parsed) ? parsed : Ui.Tone.Neutral;
        return Ui.StateDot(tone).Fill;
    }
    public object ConvertBack(object value, Type targetType, object parameter, string language)
    {
        throw new NotSupportedException();
    }
}

internal sealed class BooleanVisibilityConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
    {
        return value is true ? Visibility.Visible : Visibility.Collapsed;
    }
    public object ConvertBack(object value, Type targetType, object parameter, string language)
    {
        throw new NotSupportedException();
    }
}
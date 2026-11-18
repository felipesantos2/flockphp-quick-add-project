using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

internal sealed partial class DebuggerPane : UserControl, ISettingsPane
{
    private readonly DebuggerViewModel viewModel;

    public DebuggerPane(SettingsWindow window, RuntimeService model)
    {
        InitializeComponent();
        viewModel = new DebuggerViewModel(model);
        DataContext = viewModel;
    }

    public string Title => Strings.Settings_Pane_Debugger;
    public string Subtitle => Strings.Debugger_Subtitle;
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
}
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Mcp;

namespace Flock.Tray.ViewModels;

/// <summary>Retained state and named commands for one MCP client.</summary>
public sealed class McpClientViewModel : ObservableObject
{
    private readonly McpService service;
    private McpClient value;

    public McpClientViewModel(McpService service, McpClient value)
    {
        this.service = service;
        this.value = value;
        RegisterCommand = new AsyncRelayCommand(RegisterAsync, CanRegister);
        UnregisterCommand = new AsyncRelayCommand(UnregisterAsync, CanUnregister);
    }

    public string Id => value.Id;
    public string Name => value.Name;
    public string State => value.State;
    public string Problem => value.Problem;
    public bool IsRegistered => State == McpClientState.Registered;
    public bool IsAvailable => State == McpClientState.Available;
    public string Description => State switch
    {
        McpClientState.Registered => Strings.Ai_FlockAdded,
        McpClientState.Available => Strings.Common_Installed,
        _ => Strings.Common_NotInstalled,
    } + (Problem.Length > 0 ? " — " + Problem : "");
    public IAsyncRelayCommand RegisterCommand { get; }
    public IAsyncRelayCommand UnregisterCommand { get; }

    public void Update(McpClient next)
    {
        if (value.Id != next.Id)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Name));
        OnPropertyChanged(nameof(State));
        OnPropertyChanged(nameof(Problem));
        OnPropertyChanged(nameof(IsRegistered));
        OnPropertyChanged(nameof(IsAvailable));
        OnPropertyChanged(nameof(Description));
        RegisterCommand.NotifyCanExecuteChanged();
        UnregisterCommand.NotifyCanExecuteChanged();
    }

    private bool CanRegister() => IsAvailable && !service.IsRunning(Id);
    private bool CanUnregister() => IsRegistered && !service.IsRunning(Id);
    private Task RegisterAsync() => service.RegisterAsync(Id);
    private Task UnregisterAsync() => service.UnregisterAsync(Id);
}
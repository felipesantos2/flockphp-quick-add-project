using CommunityToolkit.Mvvm.Input;
using Flock.Kit;

namespace Flock.Tray.ViewModels;

/// <summary>
///     The selected proxy: its address and actions, then its server, HTTPS and upstream, and the
///     commands Flock starts its server with (its state, Start and Stop, and its output), or a way to
///     add them.
/// </summary>
internal sealed partial class ProxyDetailViewModel(ISiteDetailHost host, OutputViewModel output) : DetailViewModel
{
    private Status.Proxy proxy = new();
    private bool securedShown;

    public bool IsShown { get; private set; }
    public OutputViewModel OutputPanel { get; } = output;

    // Header.
    public string Name => proxy.Name;
    public string Url => proxy.Url ?? "";
    public bool HasUrl => proxy.Url is not null;
    public string OpenLabel => Localization.Format(Strings.Common_OpenTarget, proxy.Url ?? proxy.Domain);
    public string Problem => proxy.Problem ?? "";

    // Proxy: its server, HTTPS and upstream.
    public bool Running => proxy.Running;
    public string ServerLabel => proxy.ServerLabel;
    public bool IsNotRunning => !proxy.Running;
    public string NotRunningText => Localization.Format(Strings.Sites_NothingAnswersYet, proxy.UpstreamLabel);

    public bool Secured
    {
        get => securedShown;
        set
        {
            if (Refreshing || value == securedShown)
            {
                return;
            }
            // Shown again afterwards, so a refused change snaps back.
            securedShown = value;
            var name = proxy.Name;
            host.Start(name, () => host.Sites.SecureProxyAsync(name, value));
        }
    }

    public string Upstream => proxy.Upstream;
    public string Note => Localization.Format(proxy.Command is null ? Strings.Sites_ProxyFootnoteNoCommands : Strings.Sites_ProxyFootnoteCommands, proxy.Domain);

    // Commands.
    private Status.ProxyCommand? Command => proxy.Command;
    public bool HasCommands => Command is not null;
    public bool HasNoCommands => Command is null;
    public string Tone => ToneName(Command?.State switch
    {
        Status.ProxyState.Running => Ui.Tone.Success,
        Status.ProxyState.Starting or Status.ProxyState.Stopping => Ui.Tone.Caution,
        Status.ProxyState.Failed => Ui.Tone.Critical,
        _ => Ui.Tone.Neutral,
    });
    public string StateLabel => Command?.StateLabel ?? "";
    /// <summary>Why it's still starting; a failure says why in its state.</summary>
    public string StateProblem => Command is { State: Status.ProxyState.Starting } command ? command.Problem : "";
    public string StartLine => Command?.Command ?? "";
    public string StopLine => Command?.StopCommand ?? "";
    public bool HasStopLine => Command is { StopCommand.Length: > 0 };
    public string Folder => Command?.Dir ?? "";
    public bool CanStart => Command?.CanStart == true;
    public bool CanStop => Command?.CanStop == true;

    public OutputSource? Output => Command is { } command
        ? new OutputSource(command.Log, command.State is Status.ProxyState.Starting or Status.ProxyState.Running or Status.ProxyState.Stopping)
        : null;

    public void Hide()
    {
        if (IsShown)
        {
            IsShown = false;
            OnPropertyChanged(nameof(IsShown));
        }
    }

    public void Update(Status.Proxy value)
    {
        proxy = value;
        IsShown = true;
        securedShown = value.Secured;
        Refresh();
    }

    [RelayCommand]
    private void Open()
    {
        if (proxy.Url is { } url)
        {
            host.OpenUrl(url);
        }
    }

    [RelayCommand]
    private void Remove()
    {
        var current = proxy;
        host.Start(current.Name, () => host.RemoveProxyAsync(current));
    }

    [RelayCommand]
    private void ChangeUpstream()
    {
        var current = proxy;
        host.Start(current.Name, () => host.ChangeUpstreamAsync(current));
    }

    [RelayCommand]
    private void ChangeCommands()
    {
        var current = proxy;
        host.Start(current.Name, () => host.ChangeCommandAsync(current));
    }

    [RelayCommand]
    private void StartServer()
    {
        var name = proxy.Name;
        host.Start(name, () => host.Sites.StartProxyAsync(name));
    }

    [RelayCommand]
    private void StopServer()
    {
        var name = proxy.Name;
        host.Start(name, () => host.Sites.StopProxyAsync(name));
    }
}

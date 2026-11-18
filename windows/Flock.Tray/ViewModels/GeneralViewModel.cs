using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Configuration;
using Flock.Tray.Features.Update;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;
namespace Flock.Tray.ViewModels;

/// <summary>Feature-owned state and commands for the General settings pane.</summary>
public sealed partial class GeneralViewModel : ObservableObject, IDisposable
{
    private readonly ConfigurationService configuration;
    private readonly Func<bool> isRestarting;
    private readonly RuntimeService? model;
    private readonly Func<Task> restart;
    private readonly Func<string?> runtimeErrorValue;

    private readonly Func<Status?> statusValue;

    public GeneralViewModel(RuntimeService model)
        : this(model.Configuration, model.RestartAsync, () => model.IsRestarting, () => model.Status, () => model.Error ?? model.ActionError)
    {
        this.model = model;
        model.Changed += NotifyState;
        model.UpdateChanged += NotifyUpdateState;
    }

    public GeneralViewModel(ConfigurationService configuration, Func<Task>? restart = null, Func<bool>? isRestarting = null,
        Func<Status?>? status = null, Func<string?>? runtimeError = null)
    {
        this.configuration = configuration;
        this.restart = restart ?? (() => Task.CompletedTask);
        this.isRestarting = isRestarting ?? (() => false);
        statusValue = status ?? (() => null);
        runtimeErrorValue = runtimeError ?? (() => null);
        configuration.Changed += NotifyState;
    }

    [ObservableProperty]
    public partial string? SelectedParked { get; set; }

    public FlockConfig? Config => configuration.Current;
    public Status? Status => statusValue();
    public string? Error => configuration.Error ?? runtimeErrorValue();
    public bool IsBusy => configuration.IsRunning || isRestarting();
    public bool IsRestarting => isRestarting();
    public bool HasParked => Config?.Parked.Count > 0;
    public bool HasSelectedParked => SelectedParked is not null;
    public bool UpdatesEnabled => model?.UpdateService.IsEnabled == true && UpdateService.Current is not null;
    public Visibility UpdateActionsVisibility => UpdatesEnabled ? Visibility.Visible : Visibility.Collapsed;
    public string UpdateDescription
    {
        get
        {
            if (!UpdatesEnabled || model is null || UpdateService.Current is not { } current)
            {
                return Strings.General_UpdatesNeedVersion;
            }
            var version = Localization.Format(Strings.General_VersionNumber, current);
            var summary = model.UpdateService.Summary;
            return summary.Length > 0 ? $"{version} · {summary}" : version;
        }
    }
    public bool HasUpdate => model?.UpdateService.Available is not null;
    public string UpdateToText => model?.UpdateService.Available is { } update
        ? Localization.Format(Strings.General_UpdateTo, update.Version)
        : "";
    // Two buttons, since only the update itself is an accent button.
    public Visibility UpdateToVisibility => HasUpdate ? Visibility.Visible : Visibility.Collapsed;
    public Visibility CheckForUpdatesVisibility => HasUpdate ? Visibility.Collapsed : Visibility.Visible;
    public bool IsUpdateBusy => model?.UpdateService.IsBusy == true;
    public Visibility UpdateProgressVisibility => IsUpdateBusy ? Visibility.Visible : Visibility.Collapsed;
    public Visibility ReleaseNotesVisibility => model?.UpdateService is { Available: { } available, IsBusy: false }
                                                && (available.Notes.Length > 0 || available.NotesUrl is not null)
        ? Visibility.Visible
        : Visibility.Collapsed;
    public Visibility StatusVisibility => Status is null ? Visibility.Collapsed : Visibility.Visible;
    // Shown without the daemon too: General also holds the app's own settings (its language).
    public string StatusText => Status is null ? Strings.Menu_Status_NotRunning : Status.IsDegraded ? Strings.Menu_Status_NeedsAttention : Strings.Menu_Status_Running;
    public Brush StatusBrush => Status is null ? Ui.Brush("ControlStrongFillColorDisabledBrush") : Status.IsDegraded ? Ui.Caution : Ui.Success;
    public string VersionText => Status?.Version ?? "";
    public string ProcessText => Status is { } status ? Localization.Format(Strings.General_Pid, status.Pid) : "";
    public Visibility ConfigVisibility => Config is null ? Visibility.Collapsed : Visibility.Visible;
    public string DomainDescription => Status?.Dns is { Wildcard: true }
        ? Strings.General_DomainWildcard
        : Status?.Dns?.WildcardProblem is { Length: > 0 } problem
            ? Localization.Format(Strings.General_DomainHostsOnlyBecause, problem)
            : Strings.General_DomainHostsOnly;
    public string DomainValue => Config is { } config ? $"*.{config.Tld}" : "";
    public string ProjectFoldersDescription => Config is { } config
        ? Localization.Format(Strings.General_ProjectFoldersDescription, config.Tld)
        : "";
    public string Root => Config?.Root ?? "";
    public string PortsDescription => Config is { } config && (config.Ports.Http != 80 || config.Ports.Https != 443)
        ? Localization.Format(Strings.General_Ports, config.Ports.Http, config.Ports.Https)
        : "";
    public Visibility PortsVisibility => PortsDescription.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    public bool IsBeta => Config?.Channel == "beta";
    public bool BetaEnabled => Config?.Channel != "experimental";
    public Visibility ExperimentalVisibility => Config?.Channel == "experimental" ? Visibility.Visible : Visibility.Collapsed;

    public void Dispose()
    {
        configuration.Changed -= NotifyState;
        if (model is not null)
        {
            model.Changed -= NotifyState;
            model.UpdateChanged -= NotifyUpdateState;
        }
    }
    public Task RefreshAsync(CancellationToken observerToken = default)
    {
        return configuration.RefreshAsync(observerToken);
    }
    public Task SetChannelAsync(string channel)
    {
        return configuration.SetChannelAsync(channel);
    }
    public Task SetDefaultPhpAsync(string version)
    {
        return configuration.SetDefaultPhpAsync(version);
    }
    public Task SetDefaultNodeAsync(string major)
    {
        return configuration.SetDefaultNodeAsync(major);
    }

    [RelayCommand]
    private Task Refresh()
    {
        return RefreshAsync();
    }

    [RelayCommand(CanExecute = nameof(CanRestart))]
    private Task Restart()
    {
        return restart();
    }

    private bool CanRestart()
    {
        return !isRestarting();
    }

    [RelayCommand(CanExecute = nameof(CanUpdate))]
    private Task Update()
    {
        if (model is null)
        {
            return Task.CompletedTask;
        }
        return model.UpdateService.Available is null
            ? model.UpdateService.CheckAsync(userInitiated: true)
            : model.UpdateService.InstallAsync();
    }

    private bool CanUpdate()
    {
        return !IsUpdateBusy;
    }

    [RelayCommand]
    private Task SetChannel(string? value)
    {
        return value is null ? Task.CompletedTask : SetChannelAsync(value);
    }

    [RelayCommand]
    private Task SetDefaultPhp(string? value)
    {
        return value is null ? Task.CompletedTask : SetDefaultPhpAsync(value);
    }

    [RelayCommand]
    private Task SetDefaultNode(string? value)
    {
        return value is null ? Task.CompletedTask : SetDefaultNodeAsync(value);
    }

    private void NotifyState()
    {
        OnPropertyChanged(nameof(Config));
        OnPropertyChanged(nameof(Status));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsBusy));
        OnPropertyChanged(nameof(IsRestarting));
        OnPropertyChanged(nameof(HasParked));
        OnPropertyChanged(nameof(StatusVisibility));
        OnPropertyChanged(nameof(StatusText));
        OnPropertyChanged(nameof(StatusBrush));
        OnPropertyChanged(nameof(VersionText));
        OnPropertyChanged(nameof(ProcessText));
        OnPropertyChanged(nameof(ConfigVisibility));
        OnPropertyChanged(nameof(DomainDescription));
        OnPropertyChanged(nameof(DomainValue));
        OnPropertyChanged(nameof(ProjectFoldersDescription));
        OnPropertyChanged(nameof(Root));
        OnPropertyChanged(nameof(PortsDescription));
        OnPropertyChanged(nameof(PortsVisibility));
        OnPropertyChanged(nameof(IsBeta));
        OnPropertyChanged(nameof(BetaEnabled));
        OnPropertyChanged(nameof(ExperimentalVisibility));
        RestartCommand.NotifyCanExecuteChanged();
    }

    private void NotifyUpdateState()
    {
        OnPropertyChanged(nameof(UpdatesEnabled));
        OnPropertyChanged(nameof(UpdateActionsVisibility));
        OnPropertyChanged(nameof(UpdateDescription));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(UpdateToText));
        OnPropertyChanged(nameof(UpdateToVisibility));
        OnPropertyChanged(nameof(CheckForUpdatesVisibility));
        OnPropertyChanged(nameof(IsUpdateBusy));
        OnPropertyChanged(nameof(UpdateProgressVisibility));
        OnPropertyChanged(nameof(ReleaseNotesVisibility));
        UpdateCommand.NotifyCanExecuteChanged();
    }

    partial void OnSelectedParkedChanged(string? value)
    {
        OnPropertyChanged(nameof(HasSelectedParked));
    }
}

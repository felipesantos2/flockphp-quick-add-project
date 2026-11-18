using System.Collections.ObjectModel;
using System.Globalization;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.FrankenPhp;
using Flock.Tray.Features.Node;
using Flock.Tray.Features.Php;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.ViewModels;

/// <summary>Retained PHP rows and named commands for the PHP pane.</summary>
public sealed class PhpPaneViewModel : ObservableObject, IDisposable
{
    private readonly PhpService service;
    private readonly CollectionSynchronizer<string, PhpVersionViewModel> synchronizer = new();

    public PhpPaneViewModel(PhpService service)
    {
        this.service = service;
        CheckCommand = new AsyncRelayCommand(() => CheckAsync(), () => !IsChecking);
        service.Changed += RefreshRows;
        RefreshRows();
    }

    public ObservableCollection<PhpVersionViewModel> Versions { get; } = [];
    public PhpCatalog? Current => service.Current;
    public string? Error => service.Error;
    public bool IsChecking => service.IsChecking;
    public string CheckedText => Current?.CheckedAt is { } checkedAt ? Localization.Format(Strings.Versions_LastChecked, Ui.Relative(checkedAt)) : Strings.Versions_NotChecked;
    public string CheckErrorText => Current?.CheckError is { Length: > 0 } error ? Localization.Format(Strings.Versions_CouldNotCheckUpdates, error) : "";
    public IAsyncRelayCommand CheckCommand { get; }

    public Task RefreshAsync(CancellationToken token = default) => service.RefreshAsync(token);
    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) => service.CheckAsync(token);
    public void Dispose() => service.Changed -= RefreshRows;

    private void RefreshRows()
    {
        synchronizer.Synchronize(Versions, service.Current?.Versions ?? [], row => row.Minor, value => value.Minor,
            value => new PhpVersionViewModel(service, value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsChecking));
        OnPropertyChanged(nameof(CheckedText));
        OnPropertyChanged(nameof(CheckErrorText));
        CheckCommand.NotifyCanExecuteChanged();
    }
}
/// <summary>Retained state and per-minor actions for one PHP release.</summary>
public sealed class PhpVersionViewModel : ObservableObject
{
    private readonly PhpService service;
    private PhpRelease value;

    public PhpVersionViewModel(PhpService service, PhpRelease value)
    {
        this.service = service;
        this.value = value;
        InstallCommand = new AsyncRelayCommand(() => service.InstallAsync(Minor), CanRun);
        CancelCommand = new AsyncRelayCommand(() => service.CancelAsync(Minor), CanRun);
        DismissCommand = new AsyncRelayCommand(() => service.DismissAsync(Minor), CanRun);
        UninstallCommand = new AsyncRelayCommand(() => service.UninstallAsync(Minor), CanRun);
    }

    public string Minor => value.Minor;
    public PhpRelease Value => value;
    public string DisplayName => $"PHP {Minor}";
    public string Latest => value.Latest;
    public string Installed => value.Installed;
    public string VersionText => value.Update && value.Latest != value.Installed ? $"{value.Installed} → {value.Latest}" : IsInstalled ? value.Installed : value.Latest;
    public bool IsDefault => value.IsDefault;
    public bool IsInstalled => value.IsInstalled;
    public bool HasUpdate => value.Update;
    public string StatusText => value.Status switch
    {
        PhpStatus.Installed => Strings.Versions_UpToDate,
        PhpStatus.Update => value.Latest == value.Installed ? Strings.Versions_NewBuild : Strings.Versions_UpdateAvailable,
        PhpStatus.Available => Strings.Common_NotInstalled,
        _ => "",
    };
    public bool IsPrerelease => value.Prerelease;
    public bool HasXdebug => IsInstalled && value.Xdebug;
    public bool IsWithdrawn => value.Yanked.Length > 0;
    public string WithdrawnText => Localization.Format(value.Update ? Strings.Services_WithdrawnUpdate : Strings.Services_Withdrawn, value.Yanked);
    public ComponentJob? Job => value.Job;
    public bool HasJob => Job is not null;
    public bool HasRunningJob => Job?.IsRunning == true;
    public bool HasFailedJob => Job?.IsFailed == true;
    public string JobErrorText => Job?.IsFailed == true ? Job.Error.Length > 0 ? Job.Error : Strings.Versions_DownloadFailed : "";
    public double JobProgress => Job?.HasProgress == true ? Job.Progress : 0;
    public bool IsJobProgressIndeterminate => Job?.HasProgress != true;
    public string JobProgressText => Job is { } job
        ? job.PhaseText + (job.HasProgress ? " " + job.Progress.ToString("P0", CultureInfo.CurrentCulture) : "")
        : "";
    public bool CanInstall => !IsInstalled && !HasJob;
    public bool CanUpdate => IsInstalled && HasUpdate && !HasJob;
    public bool CanMakeDefault => IsInstalled && !IsDefault && !HasJob;
    public bool CanUninstall => IsInstalled && !IsDefault && !HasJob;
    public bool ShowPrimaryInstall => CanInstall;
    public bool ShowPrimaryUpdate => CanUpdate;
    public bool ShowPrimaryUninstall => CanUninstall && !HasUpdate;
    public bool ShowPrimaryCancel => HasRunningJob;
    public bool ShowPrimaryRetry => HasFailedJob;
    public bool HasMoreActions => IsInstalled || HasJob;
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand CancelCommand { get; }
    public IAsyncRelayCommand DismissCommand { get; }
    public IAsyncRelayCommand UninstallCommand { get; }

    public void Update(PhpRelease next)
    {
        if (value.Minor != next.Minor)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Latest));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(VersionText));
        OnPropertyChanged(nameof(IsDefault));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(StatusText));
        OnPropertyChanged(nameof(IsPrerelease));
        OnPropertyChanged(nameof(HasXdebug));
        OnPropertyChanged(nameof(IsWithdrawn));
        OnPropertyChanged(nameof(WithdrawnText));
        OnPropertyChanged(nameof(Job));
        OnPropertyChanged(nameof(HasJob));
        OnPropertyChanged(nameof(HasRunningJob));
        OnPropertyChanged(nameof(HasFailedJob));
        OnPropertyChanged(nameof(JobErrorText));
        OnPropertyChanged(nameof(JobProgress));
        OnPropertyChanged(nameof(IsJobProgressIndeterminate));
        OnPropertyChanged(nameof(JobProgressText));
        OnPropertyChanged(nameof(CanInstall));
        OnPropertyChanged(nameof(CanUpdate));
        OnPropertyChanged(nameof(CanMakeDefault));
        OnPropertyChanged(nameof(CanUninstall));
        OnPropertyChanged(nameof(ShowPrimaryInstall));
        OnPropertyChanged(nameof(ShowPrimaryUpdate));
        OnPropertyChanged(nameof(ShowPrimaryUninstall));
        OnPropertyChanged(nameof(ShowPrimaryCancel));
        OnPropertyChanged(nameof(ShowPrimaryRetry));
        OnPropertyChanged(nameof(HasMoreActions));
        InstallCommand.NotifyCanExecuteChanged();
        CancelCommand.NotifyCanExecuteChanged();
        DismissCommand.NotifyCanExecuteChanged();
        UninstallCommand.NotifyCanExecuteChanged();
    }

    private bool CanRun() => !service.IsRunning(Minor);
}

/// <summary>Retained FrankenPHP rows and named commands for the PHP pane.</summary>
public sealed class FrankenPhpPaneViewModel : ObservableObject, IDisposable
{
    private readonly FrankenPhpService service;
    private readonly CollectionSynchronizer<string, FrankenPhpVersionViewModel> synchronizer = new();

    public FrankenPhpPaneViewModel(FrankenPhpService service)
    {
        this.service = service;
        CheckCommand = new AsyncRelayCommand(() => CheckAsync(), () => !IsChecking);
        service.Changed += RefreshRows;
        RefreshRows();
    }

    public ObservableCollection<FrankenPhpVersionViewModel> Builds { get; } = [];
    public FrankenPhpState? Current => service.Current;
    public string? Error => service.Error;
    public bool IsChecking => service.IsChecking;
    public string CheckedText => Current?.CheckedAt is { } checkedAt ? Localization.Format(Strings.Versions_LastChecked, Ui.Relative(checkedAt)) : Strings.Versions_NotChecked;
    public string CheckErrorText => Current?.CheckError is { Length: > 0 } error ? Localization.Format(Strings.Versions_CouldNotCheckUpdates, error) : "";
    public IAsyncRelayCommand CheckCommand { get; }

    public Task RefreshAsync(CancellationToken token = default) => service.RefreshAsync(token);
    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) => service.CheckAsync(token);
    public void Dispose() => service.Changed -= RefreshRows;

    private void RefreshRows()
    {
        synchronizer.Synchronize(Builds, service.Current?.Builds ?? [], row => row.Php, value => value.Php,
            value => new FrankenPhpVersionViewModel(service, value), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsChecking));
        OnPropertyChanged(nameof(CheckedText));
        OnPropertyChanged(nameof(CheckErrorText));
        CheckCommand.NotifyCanExecuteChanged();
    }
}

/// <summary>Retained state and per-minor actions for one FrankenPHP build.</summary>
public sealed class FrankenPhpVersionViewModel : ObservableObject
{
    private readonly FrankenPhpService service;
    private FrankenPhpBuild value;

    public FrankenPhpVersionViewModel(FrankenPhpService service, FrankenPhpBuild value)
    {
        this.service = service;
        this.value = value;
        InstallCommand = new AsyncRelayCommand(() => service.InstallAsync(Php), CanRun);
        CancelCommand = new AsyncRelayCommand(() => service.CancelAsync(Php), CanRun);
        DismissCommand = new AsyncRelayCommand(() => service.DismissAsync(Php), CanRun);
        UninstallCommand = new AsyncRelayCommand(() => service.UninstallAsync(Php), CanRun);
    }

    public string Php => value.Php;
    public FrankenPhpBuild Value => value;
    public string DisplayName => $"PHP {Php}";
    public string Latest => value.Latest;
    public string Installed => value.Installed;
    public string DetailText => IsInstalled ? $"FrankenPHP {Installed} (PHP {value.PhpVersion})" : Strings.Versions_FrankenNotInstalled;
    public bool ShowDetail => !HasRunningJob && !HasFailedJob;
    public bool IsInstalled => value.IsInstalled;
    public bool HasUpdate => value.Update;
    public ComponentJob? Job => value.Job;
    public bool HasJob => Job is not null;
    public bool HasRunningJob => Job?.IsRunning == true;
    public bool HasFailedJob => Job?.IsFailed == true;
    public double JobProgress => Job?.HasProgress == true ? Job.Progress : 0;
    public bool IsJobProgressIndeterminate => Job?.HasProgress != true;
    public string JobProgressText => Job is { } job
        ? job.PhaseText + (job.HasProgress ? " " + job.Progress.ToString("P0", CultureInfo.CurrentCulture) : "")
        : "";
    public bool CanInstall => !IsInstalled && !HasJob;
    public bool CanUpdate => IsInstalled && HasUpdate && !HasJob;
    public bool CanUninstall => IsInstalled && !HasJob;
    public bool ShowPrimaryInstall => CanInstall;
    public bool ShowPrimaryUpdate => CanUpdate;
    public bool ShowPrimaryUninstall => CanUninstall && !HasUpdate;
    public bool ShowPrimaryCancel => HasRunningJob;
    public bool ShowPrimaryRetry => HasFailedJob;
    public bool HasMoreActions => HasJob || IsInstalled;
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand CancelCommand { get; }
    public IAsyncRelayCommand DismissCommand { get; }
    public IAsyncRelayCommand UninstallCommand { get; }

    public void Update(FrankenPhpBuild next)
    {
        if (value.Php != next.Php)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Latest));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(DetailText));
        OnPropertyChanged(nameof(ShowDetail));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(Job));
        OnPropertyChanged(nameof(HasJob));
        OnPropertyChanged(nameof(HasRunningJob));
        OnPropertyChanged(nameof(HasFailedJob));
        OnPropertyChanged(nameof(JobProgress));
        OnPropertyChanged(nameof(IsJobProgressIndeterminate));
        OnPropertyChanged(nameof(JobProgressText));
        OnPropertyChanged(nameof(CanInstall));
        OnPropertyChanged(nameof(CanUpdate));
        OnPropertyChanged(nameof(CanUninstall));
        OnPropertyChanged(nameof(ShowPrimaryInstall));
        OnPropertyChanged(nameof(ShowPrimaryUpdate));
        OnPropertyChanged(nameof(ShowPrimaryUninstall));
        OnPropertyChanged(nameof(ShowPrimaryCancel));
        OnPropertyChanged(nameof(ShowPrimaryRetry));
        OnPropertyChanged(nameof(HasMoreActions));
        InstallCommand.NotifyCanExecuteChanged();
        CancelCommand.NotifyCanExecuteChanged();
        DismissCommand.NotifyCanExecuteChanged();
        UninstallCommand.NotifyCanExecuteChanged();
    }

    private bool CanRun() => !service.IsRunning(Php);
}

/// <summary>Retained Node rows and named commands for the Node pane.</summary>
public sealed class NodePaneViewModel : ObservableObject, IDisposable
{
    private readonly NodeService service;
    private readonly Func<string?> defaultInstalling;
    private readonly CollectionSynchronizer<string, NodeVersionViewModel> synchronizer = new();
    private string? installingDefault;

    /// <param name="defaultInstalling">The major the first run is installing as the default, or null.</param>
    public NodePaneViewModel(NodeService service, Func<string?>? defaultInstalling = null)
    {
        this.service = service;
        this.defaultInstalling = defaultInstalling ?? (() => null);
        installingDefault = this.defaultInstalling();
        CheckCommand = new AsyncRelayCommand(() => CheckAsync(), () => !IsChecking);
        service.Changed += RefreshRows;
        RefreshRows();
    }

    public ObservableCollection<NodeVersionViewModel> Versions { get; } = [];
    public NodeCatalog? Current => service.Current;
    public string? Error => service.Error;
    public bool IsChecking => service.IsChecking;
    public bool IsEmpty => Current?.Versions.Count == 0;
    public IAsyncRelayCommand CheckCommand { get; }
    public string CheckedText => Current?.CheckedAt is { } checkedAt ? Localization.Format(Strings.Versions_LastChecked, Ui.Relative(checkedAt)) : Strings.Versions_NotChecked;
    public string CheckErrorText => Current?.CheckError is { Length: > 0 } error ? Localization.Format(Strings.Versions_CouldNotCheckUpdates, error) : "";

    public Task RefreshAsync(CancellationToken token = default) => service.RefreshAsync(token);
    public Task<OperationOutcome> CheckAsync(CancellationToken token = default) => service.CheckAsync(token);
    public void Dispose() => service.Changed -= RefreshRows;

    /// <summary>Follows the first run's install of the default; once it's done (or failed), the versions show the result.</summary>
    public void DefaultInstallChanged()
    {
        var installing = defaultInstalling();
        if (installingDefault is not null && installing != installingDefault)
        {
            _ = service.RefreshAsync();
        }
        installingDefault = installing;
        foreach (var row in Versions)
        {
            row.Update(row.Value);
        }
    }

    private void RefreshRows()
    {
        synchronizer.Synchronize(Versions, service.Current?.Versions ?? [], row => row.Major, value => value.Major,
            value => new NodeVersionViewModel(service, value, defaultInstalling), (row, value) => row.Update(value));
        OnPropertyChanged(nameof(Current));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsChecking));
        OnPropertyChanged(nameof(IsEmpty));
        OnPropertyChanged(nameof(CheckedText));
        OnPropertyChanged(nameof(CheckErrorText));
        CheckCommand.NotifyCanExecuteChanged();
    }
}

/// <summary>Retained state and per-major actions for one Node.js release.</summary>
public sealed class NodeVersionViewModel : ObservableObject
{
    private readonly NodeService service;
    private readonly Func<string?> defaultInstalling;
    private NodeRelease value;

    public NodeVersionViewModel(NodeService service, NodeRelease value, Func<string?>? defaultInstalling = null)
    {
        this.service = service;
        this.value = value;
        this.defaultInstalling = defaultInstalling ?? (() => null);
        InstallCommand = new AsyncRelayCommand(() => service.InstallAsync(Major), CanRun);
        UpdateNpmCommand = new AsyncRelayCommand(() => service.UpdateNpmAsync(Major), CanRun);
        UninstallCommand = new AsyncRelayCommand(() => service.UninstallAsync(Major), CanRun);
        SetDefaultCommand = new AsyncRelayCommand(() => service.SetDefaultAsync(Major), CanRun);
    }

    public string Major => value.Major;
    public NodeRelease Value => value;
    public string DisplayName => $"Node {Major}";
    public string Latest => value.Latest;
    public string Installed => value.Installed;
    public string Npm => value.Npm;
    public string NpmLatest => value.NpmLatest;
    public string VersionText => value.Update ? $"{value.Installed} → {value.Latest}" : value.IsInstalled ? value.Installed : value.Latest;
    public string StatusText => value.Update ? Strings.Versions_UpdateAvailable : value.IsInstalled ? Strings.Versions_UpToDate : Strings.Common_NotInstalled;
    public string NpmText => value.Npm.Length > 0 ? $"npm {value.Npm}" : "";
    public string NpmLatestText => value.NpmLatest.Length > 0 ? Localization.Format(Strings.Versions_NpmAvailable, value.NpmLatest) : "";
    public bool IsDefault => value.IsDefault;
    public bool IsInstalled => value.IsInstalled;
    public bool HasUpdate => value.Update;
    public bool IsDefaultInstalling => !IsInstalled && defaultInstalling() == Major;
    public bool IsWorking => service.IsRunning(Major) || IsDefaultInstalling;
    public bool HasRunningJob => IsWorking;
    public bool IsJobProgressIndeterminate => true;
    public string JobProgressText => service.RunningAction(Major) ?? (IsDefaultInstalling ? Localization.Format(Strings.Versions_DownloadingNode, Latest) : Strings.Common_Working);
    public bool CanInstall => !IsInstalled && !IsWorking;
    public bool CanUpdate => IsInstalled && HasUpdate && !IsWorking;
    public bool CanMakeDefault => IsInstalled && !IsDefault && !IsWorking;
    public bool CanUninstall => IsInstalled && !IsDefault && !IsWorking;
    public bool CanUpdateNpm => IsInstalled && NpmLatest.Length > 0 && !IsWorking;
    public bool ShowPrimaryInstall => CanInstall;
    public bool ShowPrimaryUpdate => CanUpdate;
    public bool ShowPrimaryUninstall => CanUninstall && !HasUpdate;
    public bool HasMoreActions => CanMakeDefault || CanUpdateNpm || (CanUninstall && HasUpdate);
    public IAsyncRelayCommand InstallCommand { get; }
    public IAsyncRelayCommand UpdateNpmCommand { get; }
    public IAsyncRelayCommand UninstallCommand { get; }
    public IAsyncRelayCommand SetDefaultCommand { get; }

    public void Update(NodeRelease next)
    {
        if (value.Major != next.Major)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Latest));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(Npm));
        OnPropertyChanged(nameof(NpmLatest));
        OnPropertyChanged(nameof(VersionText));
        OnPropertyChanged(nameof(StatusText));
        OnPropertyChanged(nameof(NpmText));
        OnPropertyChanged(nameof(NpmLatestText));
        OnPropertyChanged(nameof(IsDefault));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(HasUpdate));
        OnPropertyChanged(nameof(IsDefaultInstalling));
        OnPropertyChanged(nameof(IsWorking));
        OnPropertyChanged(nameof(HasRunningJob));
        OnPropertyChanged(nameof(IsJobProgressIndeterminate));
        OnPropertyChanged(nameof(JobProgressText));
        OnPropertyChanged(nameof(CanInstall));
        OnPropertyChanged(nameof(CanUpdate));
        OnPropertyChanged(nameof(CanMakeDefault));
        OnPropertyChanged(nameof(CanUninstall));
        OnPropertyChanged(nameof(CanUpdateNpm));
        OnPropertyChanged(nameof(ShowPrimaryInstall));
        OnPropertyChanged(nameof(ShowPrimaryUpdate));
        OnPropertyChanged(nameof(ShowPrimaryUninstall));
        OnPropertyChanged(nameof(HasMoreActions));
        InstallCommand.NotifyCanExecuteChanged();
        UpdateNpmCommand.NotifyCanExecuteChanged();
        UninstallCommand.NotifyCanExecuteChanged();
        SetDefaultCommand.NotifyCanExecuteChanged();
    }

    private bool CanRun() => !IsWorking;
}

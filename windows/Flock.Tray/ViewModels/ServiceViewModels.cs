using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using Flock.Kit;
using Flock.Tray.Features.Services;
using Flock.Tray.Infrastructure;
using CatalogEntry = Flock.Kit.CatalogService;
using CatalogFeatureService = Flock.Tray.Features.Services.CatalogService;

namespace Flock.Tray.ViewModels;

/// <summary>Retained service rows and the three shared service snapshots used by the Services pane.</summary>
public sealed partial class ServicesPaneViewModel : ObservableObject, IDisposable
{
    private readonly ServicesService services;
    private readonly PostgresService postgres;
    private readonly CatalogFeatureService catalog;
    private readonly ServiceReleaseActions releaseActions;
    private readonly CollectionSynchronizer<string, ServiceViewModel> serviceSynchronizer = new();
    private readonly CollectionSynchronizer<string, CatalogServiceViewModel> catalogSynchronizer = new();
    private string? postgresDraftMajor;
    private int postgresDraftPort;

    public ServicesPaneViewModel(ServicesService services, PostgresService postgres, CatalogFeatureService catalog, ServiceReleaseActions releaseActions)
    {
        this.services = services;
        this.postgres = postgres;
        this.catalog = catalog;
        this.releaseActions = releaseActions;
        services.Changed += ServicesChanged;
        postgres.Changed += PostgresChanged;
        catalog.Changed += CatalogChanged;
        RefreshRows();
    }

    public ObservableCollection<ServiceViewModel> Services { get; } = [];
    public ObservableCollection<CatalogServiceViewModel> CatalogServices { get; } = [];
    public PostgresVersions? Postgres => postgres.Current;
    public ServiceCatalog? Catalog => catalog.Current;
    public ServiceReleaseActions ReleaseActions => releaseActions;
    public string? Error => services.Error ?? postgres.Error ?? catalog.Error;
    public bool IsPostgresRunning => postgres.IsRunning;
    public bool IsCatalogChecking => catalog.IsChecking;
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(VisibleServices))]
    [NotifyPropertyChangedFor(nameof(VisibleServiceGroups))]
    [NotifyPropertyChangedFor(nameof(SelectedService))]
    public partial bool ShowAll { get; set; } = true;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(VisibleServices))]
    [NotifyPropertyChangedFor(nameof(SelectedService))]
    public partial string? SelectedId { get; set; }

    public IEnumerable<ServiceViewModel> VisibleServices => ShowAll
        ? Services
        : Services.Where(service => !service.IsOnRequest || service.Installed);
    public IEnumerable<ServiceGroupViewModel> VisibleServiceGroups => VisibleServices
        .GroupBy(service => service.Category)
        .Select(group => new ServiceGroupViewModel(group.Key, [.. group]));
    public ServiceViewModel? SelectedService => VisibleServices.FirstOrDefault(service => service.Id == SelectedId);
    public event Action<ServiceViewModel?>? SelectedServiceChanged;
    public string? PostgresDraftMajor
    {
        get => postgresDraftMajor;
        set => SetProperty(ref postgresDraftMajor, value);
    }
    public int PostgresDraftPort
    {
        get => postgresDraftPort == 0 ? Postgres?.Port ?? 0 : postgresDraftPort;
        set
        {
            SetProperty(ref postgresDraftPort, value);
        }
    }

    public Task RefreshAsync(CancellationToken observerToken = default, CancellationToken lifetimeToken = default) =>
        Task.WhenAll(services.RefreshAsync(observerToken, lifetimeToken), postgres.RefreshAsync(observerToken, lifetimeToken), catalog.RefreshAsync(observerToken, lifetimeToken));

    public async Task RefreshReleaseStateAsync(CancellationToken token = default)
    {
        await postgres.RefreshAsync(token);
        await catalog.RefreshAsync(token);
    }

    public bool IsServiceRunning(string id) => services.IsRunning(id);
    public bool IsPostgresBusy => postgres.Current?.AnyBusy == true || postgres.IsRunning;
    public bool IsCatalogBusy(string id) => catalog.IsRunning(id) || catalog.Current?[id]?.Job?.IsRunning == true;
    public Task<OperationOutcome> CheckPostgresAsync(CancellationToken token = default) => postgres.CheckAsync(token);
    public Task<OperationOutcome> CheckCatalogAsync(CancellationToken token = default) => catalog.CheckAsync(token);

    public void Dispose()
    {
        services.Changed -= ServicesChanged;
        postgres.Changed -= PostgresChanged;
        catalog.Changed -= CatalogChanged;
    }

    private void ServicesChanged() => RefreshRows(detailMayHaveChanged: true);

    private void PostgresChanged() => RefreshRows(detailMayHaveChanged: SelectedId == "postgres");

    private void CatalogChanged() => RefreshRows(detailMayHaveChanged: SelectedService?.IsOnRequest == true);

    private void RefreshRows(bool detailMayHaveChanged = false)
    {
        var selected = SelectedService;
        var selectedSnapshot = selected?.Value;
        serviceSynchronizer.Synchronize(Services, services.Current, row => row.Id, value => value.Id,
            value => new ServiceViewModel(value), (row, value) => row.Update(value));
        catalogSynchronizer.Synchronize(CatalogServices, catalog.Current?.Services ?? [], row => row.Id, value => value.Id,
            value => new CatalogServiceViewModel(value), (row, value) => row.Update(value));
        NormalizeInstallDrafts();
        OnPropertyChanged(nameof(Postgres));
        OnPropertyChanged(nameof(Catalog));
        OnPropertyChanged(nameof(Error));
        OnPropertyChanged(nameof(IsPostgresRunning));
        OnPropertyChanged(nameof(IsCatalogChecking));
        OnPropertyChanged(nameof(IsPostgresBusy));
        OnPropertyChanged(nameof(VisibleServices));
        OnPropertyChanged(nameof(VisibleServiceGroups));
        NormalizeSelection();
        var current = SelectedService;
        if (selected is not null && ReferenceEquals(selected, current) &&
            (detailMayHaveChanged || !ReferenceEquals(selectedSnapshot, current?.Value)))
        {
            SelectedServiceChanged?.Invoke(current);
        }
    }

    public void NotifySelectedServiceChanged() => SelectedServiceChanged?.Invoke(SelectedService);

    partial void OnShowAllChanged(bool value) => NormalizeSelection();

    partial void OnSelectedIdChanged(string? value) => SelectedServiceChanged?.Invoke(SelectedService);

    private void NormalizeSelection()
    {
        var selected = VisibleServices.FirstOrDefault(service => service.Id == SelectedId)?.Id;
        var next = selected ?? VisibleServices.FirstOrDefault()?.Id;
        if (SelectedId != next)
        {
            SelectedId = next;
        }
    }

    private void NormalizeInstallDrafts()
    {
        var postgresTargets = releaseActions.InstallTargets("postgres");
        var postgresDraft = NormalizeDraft(postgresDraftMajor, postgresTargets);
        if (postgresDraftMajor != postgresDraft)
        {
            postgresDraftMajor = postgresDraft;
            OnPropertyChanged(nameof(PostgresDraftMajor));
        }

        foreach (var service in CatalogServices)
        {
            service.NormalizeDraft(releaseActions.InstallTargets(service.Id));
        }
    }

    private static string? NormalizeDraft(string? draft, IReadOnlyList<ServiceReleaseTarget> targets) =>
        targets.Any(target => target.Line == draft) ? draft : targets.FirstOrDefault()?.Line;
}

/// <summary>One visible service category and its retained rows.</summary>
public sealed class ServiceGroupViewModel(string name, IReadOnlyList<ServiceViewModel> services)
{
    /// <summary>The category in the app's language; one the app doesn't know shows as the daemon sends it.</summary>
    public string Name { get; } = name switch
    {
        "Core" => Strings.Services_Category_Core,
        "Database" => Strings.Services_Category_Database,
        "Cache & Queue" => Strings.Services_Category_CacheQueue,
        "Search" => Strings.Services_Category_Search,
        "Storage" => Strings.Services_Category_Storage,
        "Mail" => Strings.Services_Category_Mail,
        "Debugging" => Strings.Services_Category_Debugging,
        _ => name,
    };
    public IReadOnlyList<ServiceViewModel> Services { get; } = services;
}

/// <summary>Retained state for one daemon service.</summary>
public sealed class ServiceViewModel : ObservableObject
{
    private Service value;

    public ServiceViewModel(Service value)
    {
        this.value = value;
    }

    public string Id => value.Id;
    public Service Value => value;
    public string Name => value.Name;
    public string Category => value.Category;
    public string Description => value.Description;
    public bool Available => value.Available;
    public string Unavailable => value.Unavailable;
    public string Version => value.Version;
    public int Port => value.Port;
    public string ListDetail => string.Join(" · ", new[]
    {
        value.Version,
        value.Port > 0 ? Localization.Format(Strings.Services_PortNumber, value.Port) : null,
        !value.Running ? Strings.Services_Postgres_Stopped : null,
    }.Where(part => !string.IsNullOrEmpty(part)));
    public bool Installed => value.Installed;
    public bool Enabled => value.Enabled;
    public bool Running => value.Running;
    public bool Stoppable => value.Stoppable;
    public string Env => value.Env;
    public string Log => value.Log;
    public string Url => value.Url;
    public string Problem => value.Problem;
    public bool IsOnRequest => value.IsOnRequest;
    public bool HasConfigurablePort => value.HasConfigurablePort;
    public bool IsDatabase => value.IsDatabase;
    public void Update(Service next)
    {
        if (value.Id != next.Id)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Name));
        OnPropertyChanged(nameof(Category));
        OnPropertyChanged(nameof(Description));
        OnPropertyChanged(nameof(Available));
        OnPropertyChanged(nameof(Unavailable));
        OnPropertyChanged(nameof(Version));
        OnPropertyChanged(nameof(Port));
        OnPropertyChanged(nameof(ListDetail));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(Enabled));
        OnPropertyChanged(nameof(Running));
        OnPropertyChanged(nameof(Stoppable));
        OnPropertyChanged(nameof(Env));
        OnPropertyChanged(nameof(Log));
        OnPropertyChanged(nameof(Url));
        OnPropertyChanged(nameof(Problem));
    }
}

/// <summary>Retained catalog service state and install form drafts.</summary>
public sealed class CatalogServiceViewModel : ObservableObject
{
    private CatalogEntry value;
    private string? draftLine;
    private int draftPort;

    public CatalogServiceViewModel(CatalogEntry value)
    {
        this.value = value;
        draftLine = value.PreferredLine?.Id;
        draftPort = value.Port;
    }

    public string Id => value.Id;
    public CatalogEntry Value => value;
    public string Name => value.Name;
    public string Category => value.Category;
    public string Description => value.Description;
    public bool Available => value.Available;
    public string Unavailable => value.Unavailable;
    public string Estimate => value.Estimate;
    public bool IsInstalled => value.IsInstalled;
    public string Line => value.Line;
    public string Installed => value.Installed;
    public int Port => value.Port;
    public int DefaultPort => value.DefaultPort;
    public IReadOnlyList<CatalogLine> Lines => value.Lines;
    public CatalogJob? Job => value.Job;
    public string? DraftLine
    {
        get => draftLine;
        set
        {
            SetProperty(ref draftLine, value);
        }
    }
    public int DraftPort
    {
        get => draftPort;
        set
        {
            SetProperty(ref draftPort, value);
        }
    }
    public void Update(CatalogEntry next)
    {
        if (value.Id != next.Id)
        {
            return;
        }
        value = next;
        OnPropertyChanged(nameof(Name));
        OnPropertyChanged(nameof(Category));
        OnPropertyChanged(nameof(Description));
        OnPropertyChanged(nameof(Available));
        OnPropertyChanged(nameof(Unavailable));
        OnPropertyChanged(nameof(Estimate));
        OnPropertyChanged(nameof(IsInstalled));
        OnPropertyChanged(nameof(Line));
        OnPropertyChanged(nameof(Installed));
        OnPropertyChanged(nameof(Port));
        OnPropertyChanged(nameof(DefaultPort));
        OnPropertyChanged(nameof(Lines));
        OnPropertyChanged(nameof(Job));
    }

    public void NormalizeDraft(IReadOnlyList<ServiceReleaseTarget> targets)
    {
        var normalized = targets.Any(target => target.Line == draftLine) ? draftLine : targets.FirstOrDefault()?.Line;
        if (draftLine != normalized)
        {
            draftLine = normalized;
            OnPropertyChanged(nameof(DraftLine));
        }
    }
}

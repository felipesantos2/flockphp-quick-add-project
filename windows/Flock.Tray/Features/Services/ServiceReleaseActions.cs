using Flock.Kit;
using Flock.Tray.Infrastructure;

namespace Flock.Tray.Features.Services;

public enum ServiceReleaseActionKind { Unavailable, Loading, Install, Check, Update, UpdateAll, Busy }

public enum ServiceRowCommandKind { Install, Update, Activate, Uninstall, Reinstall, ShowLog, Cancel, Retry, Dismiss }

public enum ServiceReleaseSource { Postgres, Catalog }

public sealed record ServiceReleaseTarget(string ServiceId, ServiceReleaseSource Source, string Line, string Label, string Latest);

public sealed record ServiceReleaseAction(
    ServiceReleaseActionKind Kind,
    string AccessibleLabel,
    bool IsEnabled,
    DateTimeOffset? CheckedAt,
    string CheckError,
    IReadOnlyList<ServiceReleaseTarget> Targets,
    string? BusyText);

public sealed record ServiceReleaseLine(
    ServiceReleaseTarget Target,
    bool IsInstalled,
    bool IsActive,
    bool HasUpdate,
    string Facts,
    string? BusyText,
    IReadOnlyList<ServiceRowCommandKind> PrimaryCommands,
    IReadOnlyList<ServiceRowCommandKind> MoreCommands);

public sealed record ServiceReleaseRow(
    string ServiceId,
    ServiceReleaseSource Source,
    bool IsAvailable,
    int Port,
    int DefaultPort,
    string Estimate,
    IReadOnlyList<ServiceReleaseLine> Lines,
    CatalogJob? Job,
    IReadOnlyList<ServiceRowCommandKind> JobCommands);

/// <summary>Resolves and dispatches the release action shown in the Services pane.</summary>
public sealed class ServiceReleaseActions(PostgresService postgres, CatalogService catalog)
{

    public ServiceReleaseAction Resolve(string serviceId) => serviceId == "postgres" ? ResolvePostgres() : ResolveCatalog(serviceId);

    /// <summary>Returns the current uninstalled release targets in their snapshot order.</summary>
    public IReadOnlyList<ServiceReleaseTarget> InstallTargets(string serviceId) => ResolveRow(serviceId).Lines
        .Where(line => !line.IsInstalled)
        .Select(line => line.Target)
        .ToList();

    public ServiceReleaseRow ResolveRow(string serviceId)
    {
        if (serviceId == "postgres")
        {
            return postgres.Current is not { } versions
                ? new ServiceReleaseRow(serviceId, ServiceReleaseSource.Postgres, true, 0, 0, "", [], null, [])
                : new ServiceReleaseRow("postgres", ServiceReleaseSource.Postgres, true, versions.Port, 0, "", versions.Majors.Select(ResolvePostgresLine).ToList(), null, []);
        }

        var entry = catalog.Current?[serviceId];
        if (entry is null || !entry.Available)
        {
            return new ServiceReleaseRow(serviceId, ServiceReleaseSource.Catalog, false, 0, 0, "", [], null, []);
        }

        var hasJob = entry.Job is { IsRunning: true } or { IsFailed: true };
        var lines = entry.Lines.Select(line => ResolveCatalogLine(entry, line, hasJob)).ToList();
        return new ServiceReleaseRow(serviceId, ServiceReleaseSource.Catalog, true, entry.Port, entry.DefaultPort, entry.Estimate, lines, entry.Job,
            ResolveCatalogJobCommands(entry));
    }

    public Task<OperationOutcome> InstallFirstAsync(string serviceId, string line, int? port, int currentPort, CancellationToken token = default) =>
        serviceId == "postgres"
            ? postgres.InstallFirstAsync(line, port, currentPort, token)
            : catalog.InstallAsync(serviceId, line, port, token);

    public Task<OperationOutcome> RunTargetAsync(ServiceReleaseTarget target, CancellationToken token = default) =>
        target.Source == ServiceReleaseSource.Postgres
            ? RunAsync(target, postgres.Current?.Majors.FirstOrDefault(major => major.Major == target.Line)?.IsInstalled == true
                ? ServiceRowCommandKind.Update
                : ServiceRowCommandKind.Install, token)
            : RunAsync(target, catalog.Current?[target.ServiceId]?.Lines.FirstOrDefault(line => line.Id == target.Line)?.Installed == true
                ? ServiceRowCommandKind.Update
                : ServiceRowCommandKind.Install, token);

    public Task<OperationOutcome> RunAsync(ServiceReleaseTarget target, ServiceRowCommandKind command, CancellationToken token = default) =>
        RunAsync(target, command, deleteData: false, token);

    public Task<OperationOutcome> RunAsync(ServiceReleaseTarget target, ServiceRowCommandKind command, bool deleteData, CancellationToken token = default)
    {
        var row = ResolveRow(target.ServiceId);
        var line = row.Lines.FirstOrDefault(candidate => candidate.Target.Line == target.Line &&
            candidate.Target.Source == target.Source && candidate.Target.ServiceId == target.ServiceId);
        var isLineCommand = line is not null && line.PrimaryCommands.Concat(line.MoreCommands).Contains(command);
        var isJobCommand = target.Source == ServiceReleaseSource.Catalog && row.Job is { } job &&
            row.JobCommands.Contains(command) && target.Line == job.Line;
        if (!isLineCommand && !isJobCommand)
        {
            return Task.FromResult(OperationOutcome.Rejected);
        }

        return target.Source switch
        {
            ServiceReleaseSource.Postgres => RunPostgresAsync(line!.Target, command, deleteData, token),
            ServiceReleaseSource.Catalog => RunCatalogAsync(target, row.Job, command, deleteData, token),
            _ => Task.FromResult(OperationOutcome.Rejected),
        };
    }

    public async Task<OperationOutcome> RunAllUpdatesAsync(string serviceId, CancellationToken token = default)
    {
        if (serviceId == "postgres")
        {
            var targets = postgres.Current?.Majors
                .Where(major => major.IsInstalled && major.Update)
                .Select(major => new ServiceReleaseTarget("postgres", ServiceReleaseSource.Postgres, major.Major, major.Major, major.Latest))
                .ToList() ?? [];
            if (targets.Count < 2)
            {
                return OperationOutcome.Rejected;
            }
            foreach (var target in targets)
            {
                var outcome = await postgres.UpdateAsync(target.Line, token);
                if (outcome != OperationOutcome.Succeeded)
                {
                    return outcome;
                }
            }
            return OperationOutcome.Succeeded;
        }

        var entry = catalog.Current?[serviceId];
        var lines = entry?.Lines
            .Where(line => line.Installed && line.Update)
            .Select(line => line.Id)
            .ToList() ?? [];
        return lines.Count < 2 ? OperationOutcome.Rejected : await catalog.InstallUpdatesAsync(serviceId, lines, token);
    }

    public Task<OperationOutcome> CheckAsync(string serviceId, CancellationToken token = default) =>
        serviceId == "postgres" ? postgres.CheckAsync(token) : catalog.CheckAsync(token);

    public Task<OperationOutcome> InstallDeepLinkAsync(string serviceId, string line, CancellationToken token = default) =>
        serviceId == "postgres"
            ? postgres.InstallAsync(line, token)
            : catalog.InstallAsync(serviceId, line, token: token);

    private ServiceReleaseAction ResolvePostgres()
    {
        var versions = postgres.Current;
        if (!postgres.HasSnapshot || versions is null)
        {
            return Loading(null, Strings.Services_Releases_LoadingPostgres);
        }
        if (postgres.IsRunning || versions.AnyBusy)
        {
            return Busy(versions.CheckedAt, versions.CheckError, postgres.IsChecking && !versions.AnyBusy ? Strings.Versions_Checking : BusyText(versions));
        }

        var targets = InstallTargets("postgres");
        if (!versions.AnyInstalled && targets.Count > 0)
        {
            return new ServiceReleaseAction(ServiceReleaseActionKind.Install, InstallLabel("PostgreSQL", targets[0]), true,
                versions.CheckedAt, versions.CheckError, targets, null);
        }

        var updates = ResolveRow("postgres").Lines.Where(line => line.HasUpdate).Select(line => line.Target).ToList();
        if (updates.Count == 1)
        {
            return new ServiceReleaseAction(ServiceReleaseActionKind.Update, Strings.Services_Releases_InstallUpdate, true,
                versions.CheckedAt, versions.CheckError, updates, null);
        }
        if (updates.Count > 1)
        {
            return new ServiceReleaseAction(ServiceReleaseActionKind.UpdateAll, Localization.Format(Strings.Services_Releases_InstallAllUpdates, updates.Count), true,
                versions.CheckedAt, versions.CheckError, updates, null);
        }
        return Check(versions.CheckedAt, versions.CheckError);
    }

    private ServiceReleaseAction ResolveCatalog(string serviceId)
    {
        var snapshot = catalog.Current;
        if (!catalog.HasSnapshot || snapshot is null)
        {
            return Loading(null, Strings.Services_Releases_LoadingServices);
        }
        var entry = snapshot[serviceId];
        if (entry is null || !entry.Available)
        {
            return new ServiceReleaseAction(ServiceReleaseActionKind.Unavailable, Strings.Services_Releases_Unavailable, false,
                snapshot.CheckedAt, snapshot.CheckError, [], null);
        }
        if (catalog.IsRunning(serviceId) || catalog.IsChecking || entry.Job?.IsRunning == true)
        {
            return Busy(snapshot.CheckedAt, snapshot.CheckError, catalog.IsChecking && !catalog.IsRunning(serviceId) && entry.Job?.IsRunning != true
                ? Strings.Versions_Checking : BusyText(entry));
        }

        var targets = InstallTargets(serviceId);
        if (!entry.Lines.Any(line => line.Installed) && targets.Count > 0)
        {
            return new ServiceReleaseAction(ServiceReleaseActionKind.Install, InstallLabel(entry.Name, targets[0]), true,
                snapshot.CheckedAt, snapshot.CheckError, targets, null);
        }

        var updates = entry.Lines
            .Where(line => line.Installed && line.Update)
            .Select(line => new ServiceReleaseTarget(serviceId, ServiceReleaseSource.Catalog, line.Id, line.Label, line.Latest))
            .ToList();
        if (updates.Count == 1)
        {
            return new ServiceReleaseAction(ServiceReleaseActionKind.Update, Strings.Services_Releases_InstallUpdate, true,
                snapshot.CheckedAt, snapshot.CheckError, updates, null);
        }
        if (updates.Count > 1)
        {
            return new ServiceReleaseAction(ServiceReleaseActionKind.UpdateAll, Localization.Format(Strings.Services_Releases_InstallAllUpdates, updates.Count), true,
                snapshot.CheckedAt, snapshot.CheckError, updates, null);
        }
        return Check(snapshot.CheckedAt, snapshot.CheckError);
    }

    private static ServiceReleaseAction Loading(DateTimeOffset? checkedAt, string label) =>
        new(ServiceReleaseActionKind.Loading, label, false, checkedAt, "", [], null);

    private static ServiceReleaseAction Busy(DateTimeOffset? checkedAt, string checkError, string busyText) =>
        new(ServiceReleaseActionKind.Busy, busyText, false, checkedAt, checkError, [], busyText);

    private static ServiceReleaseAction Check(DateTimeOffset? checkedAt, string checkError) =>
        new(ServiceReleaseActionKind.Check, Strings.General_CheckForUpdates, true, checkedAt, checkError, [], null);

    private static string InstallLabel(string serviceName, ServiceReleaseTarget target) =>
        Localization.Format(Strings.Services_InstallName, target.Latest.Length > 0 ? $"{serviceName} {target.Latest}" : serviceName);

    private static ServiceReleaseLine ResolvePostgresLine(PostgresMajor major)
    {
        var target = new ServiceReleaseTarget("postgres", ServiceReleaseSource.Postgres, major.Major, major.Major, major.Latest);
        var primary = new List<ServiceRowCommandKind>();
        var more = new List<ServiceRowCommandKind>();
        if (!major.IsBusy)
        {
            if (!major.IsInstalled)
            {
                primary.Add(ServiceRowCommandKind.Install);
            }
            else if (major.Update)
            {
                primary.Add(ServiceRowCommandKind.Update);
                more.Add(ServiceRowCommandKind.Uninstall);
                if (!major.Active)
                {
                    more.Add(ServiceRowCommandKind.Activate);
                }
            }
            else
            {
                primary.Add(ServiceRowCommandKind.Uninstall);
                if (!major.Active)
                {
                    more.Add(ServiceRowCommandKind.Activate);
                }
            }
        }

        var facts = new List<string> { major.IsInstalled ? major.Installed : Strings.Common_NotInstalled };
        if (major.Update)
        {
            facts.Add(major.Latest == major.Installed ? Strings.Services_Releases_NewBuildAvailable : Localization.Format(Strings.Services_Releases_Available, major.Latest));
        }
        if (major.Yanked.Length > 0)
        {
            facts.Add(Localization.Format(Strings.Services_Releases_Withdrawn, major.Yanked));
        }
        if (major.Prerelease)
        {
            facts.Add(Localization.Format(Strings.Services_Releases_Prerelease, major.Latest));
        }
        if (major.HasData)
        {
            facts.Add(major.IsInstalled
                ? Localization.Plural(Strings.ResourceManager, "Services_Releases_BytesOfData", major.DataSize)
                : Localization.Plural(Strings.ResourceManager, "Services_Releases_DatabasesKept", major.DataSize));
        }
        return new ServiceReleaseLine(target, major.IsInstalled, major.Active, major.Update, string.Join(" · ", facts),
            major.IsBusy ? major.BusyText : null, primary, more);
    }

    private static ServiceReleaseLine ResolveCatalogLine(Flock.Kit.CatalogService service, CatalogLine line, bool hasJob)
    {
        var target = new ServiceReleaseTarget(service.Id, ServiceReleaseSource.Catalog, line.Id, line.Label, line.Latest);
        var primary = new List<ServiceRowCommandKind>();
        var more = new List<ServiceRowCommandKind>();
        if (!hasJob)
        {
            if (!line.Installed)
            {
                primary.Add(ServiceRowCommandKind.Install);
            }
            else if (line.Update)
            {
                primary.Add(ServiceRowCommandKind.Update);
                more.Add(ServiceRowCommandKind.Uninstall);
                if (!line.Active)
                {
                    more.Add(ServiceRowCommandKind.Activate);
                }
                more.Add(ServiceRowCommandKind.ShowLog);
            }
            else
            {
                primary.Add(ServiceRowCommandKind.Uninstall);
                if (!line.Active)
                {
                    more.Add(ServiceRowCommandKind.Activate);
                }
                more.Add(ServiceRowCommandKind.Reinstall);
                more.Add(ServiceRowCommandKind.ShowLog);
            }
        }

        var facts = new List<string> { line.Installed ? line.Version : Strings.Common_NotInstalled };
        if (!line.Installed && line.Latest.Length > 0)
        {
            facts.Add(Localization.Format(Strings.Services_Releases_Available, line.Latest));
        }
        if (line.Update)
        {
            facts.Add(line.Latest == line.Version ? Strings.Services_Releases_NewBuildAvailable : Localization.Format(Strings.Services_Releases_Available, line.Latest));
        }
        else if (line.Installed)
        {
            facts.Add(Strings.Services_Releases_UpToDate);
        }
        if (line.HasData)
        {
            facts.Add(line.Installed ? Strings.Services_Releases_HasData : Strings.Services_Releases_DataKept);
        }
        return new ServiceReleaseLine(target, line.Installed, line.Active, line.Update, string.Join(" · ", facts), null, primary, more);
    }

    private IReadOnlyList<ServiceRowCommandKind> ResolveCatalogJobCommands(Flock.Kit.CatalogService entry)
    {
        if (entry.Job?.IsRunning == true)
        {
            return catalog.CanCancelJob(entry.Id)
                ? [ServiceRowCommandKind.ShowLog, ServiceRowCommandKind.Cancel]
                : [ServiceRowCommandKind.ShowLog];
        }
        return entry.Job?.IsFailed == true
            ? [ServiceRowCommandKind.Retry, ServiceRowCommandKind.Dismiss, ServiceRowCommandKind.ShowLog]
            : [];
    }

    private Task<OperationOutcome> RunPostgresAsync(ServiceReleaseTarget target, ServiceRowCommandKind command, bool deleteData, CancellationToken token) => command switch
    {
        ServiceRowCommandKind.Install => postgres.InstallAsync(target.Line, token),
        ServiceRowCommandKind.Update => postgres.UpdateAsync(target.Line, token),
        ServiceRowCommandKind.Activate => postgres.ActivateAsync(target.Line, token),
        ServiceRowCommandKind.Uninstall => postgres.UninstallAsync(target.Line, deleteData, token),
        _ => Task.FromResult(OperationOutcome.Rejected),
    };

    private Task<OperationOutcome> RunCatalogAsync(ServiceReleaseTarget target, CatalogJob? job, ServiceRowCommandKind command, bool deleteData, CancellationToken token) => command switch
    {
        ServiceRowCommandKind.Install or ServiceRowCommandKind.Update or ServiceRowCommandKind.Reinstall => catalog.InstallAsync(target.ServiceId, target.Line, token: token),
        ServiceRowCommandKind.Activate => catalog.ActivateAsync(target.ServiceId, target.Line, token),
        ServiceRowCommandKind.Uninstall => catalog.UninstallAsync(target.ServiceId, target.Line, deleteData, token),
        ServiceRowCommandKind.Cancel => catalog.CancelAsync(target.ServiceId, token),
        ServiceRowCommandKind.Dismiss => catalog.DismissAsync(target.ServiceId, token),
        ServiceRowCommandKind.Retry when job is { IsFailed: true } && job.Line.Length > 0 => catalog.InstallAsync(target.ServiceId, job.Line, token: token),
        _ => Task.FromResult(OperationOutcome.Rejected),
    };

    private static string BusyText(PostgresVersions versions) =>
        versions.Majors.FirstOrDefault(major => major.IsBusy)?.BusyText ?? Strings.Common_Working;

    private static string BusyText(Flock.Kit.CatalogService service) => service.Job?.PhaseText ?? Strings.Common_Working;
}

using System.Globalization;
using Flock.Kit;
using Flock.Tray.Features.Services;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using CatalogService = Flock.Kit.CatalogService;
namespace Flock.Tray.Settings;

/// <summary>
///     The catalog services (MySQL, MariaDB, Redis, MongoDB, Meilisearch, RustFS): pick a version line
///     and a port and install, follow the install (a job in the daemon); like PostgreSQL's majors,
///     lines install side by side: install more, switch, update or uninstall them. Each keeps its own data.
/// </summary>
internal sealed partial class ServicesPane
{
    private bool CatalogWorking(string id)
    {
        return Model.Catalog.IsRunning(id) || Model.Catalog.Current?[id]?.Job?.IsRunning == true;
    }

    private bool CatalogFailed(Service service)
    {
        return Model.Catalog.Current?[service.Id]?.Job?.IsFailed == true;
    }

    private void CatalogDetail(StackPanel panel, Service service)
    {
        if (Model.Catalog.Current is not { } catalog)
        {
            panel.Children.Add(LoadingRow(Strings.Common_Loading));
            return;
        }
        if (catalog[service.Id] is not { } entry)
        {
            panel.Children.Add(Ui.Caption(Localization.Format(Strings.Services_DaemonDoesNotOffer, service.Name)));
            return;
        }
        var row = Model.ReleaseActions.ResolveRow(entry.Id);
        if (!row.IsAvailable)
        {
            panel.Children.Add(Ui.Caption(entry.Unavailable.Length > 0 ? entry.Unavailable : Localization.Format(Strings.Services_NotOnThisComputer, entry.Name)));
            panel.Children.Add(CheckFooter(service.Id, Model.ReleaseActions.Resolve(service.Id)));
            return;
        }
        if (row.Job is { } job)
        {
            panel.Children.Add(CatalogJobCard(entry, row, job));
        }
        if (!entry.Lines.Any(l => l.Installed))
        {
            if (entry.Job is null && !Model.Catalog.IsRunning(entry.Id))
            {
                var targets = Model.ReleaseActions.InstallTargets(entry.Id);
                panel.Children.Add(targets.Count == 0
                    ? Inner(Ui.Caption(Localization.Format(Strings.Services_NoVersions, entry.Name)))
                    : CatalogInstallCard(entry, row, targets));
            }
        }
        else
        {
            panel.Children.Add(CatalogLines(entry, row));
        }
        panel.Children.Add(CheckFooter(service.Id, Model.ReleaseActions.Resolve(service.Id)));
    }

    /// <summary>What it is, the version line and port, and Install (see InstallCard).</summary>
    private Border CatalogInstallCard(CatalogService entry, ServiceReleaseRow row, IReadOnlyList<ServiceReleaseTarget> targets)
    {
        return InstallCard(entry.Id, entry.Name, entry.Description.Length > 0 ? entry.Description : Strings.Services_RunsForSites,
            targets, row.Port, row.Port != row.DefaultPort ? Localization.Format(Strings.Services_PortTaken, row.DefaultPort, row.Port) : null, row.Estimate);
    }

    /// <summary>A running or failed install.</summary>
    private Border CatalogJobCard(CatalogService entry, ServiceReleaseRow row, CatalogJob job)
    {
        var content = Ui.Column(8);
        var actions = Ui.Row(8);
        var target = job.Version.Length > 0 ? $"{entry.Name} {job.Version}" : entry.Name;
        if (job.IsRunning)
        {
            var heading = Localization.Format(job.Action == "update" ? Strings.Services_UpdatingTarget : Strings.Services_InstallingTarget, target);
            var percent = job.HasProgress ? job.Progress.ToString("P0", CultureInfo.CurrentCulture) : "";
            content.Children.Add(Ui.Spread(Ui.Text(heading, "BodyStrongTextBlockStyle"), Ui.Caption(percent, wrap: false)));
            var phase = job.PhaseText + (job.StartedAt is { Year: > 2000 } started ? " · " + Localization.Format(Strings.Services_Started, Ui.Relative(started)) : "");
            content.Children.Add(new Controls.OperationProgress { Text = phase, Value = job.Progress, IsIndeterminate = !job.HasProgress });
            foreach (var command in row.JobCommands)
            {
                actions.Children.Add(CatalogJobCommand(entry, job, command));
            }
            content.Children.Add(actions);
            return Inner(content);
        }

        content.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 14, Ui.Critical),
            Ui.Text(Localization.Format(job.Action == "update" ? Strings.Services_UpdatingFailed : Strings.Services_InstallingFailed,
                job.Action == "update" ? entry.Name : target), "BodyStrongTextBlockStyle")));
        var error = Ui.Caption(job.Error.Length > 0 ? job.Error : Strings.Services_NoDetails, Ui.Critical);
        error.IsTextSelectionEnabled = true;
        error.MaxLines = 4;
        ToolTipService.SetToolTip(error, job.Error);
        content.Children.Add(error);
        foreach (var command in row.JobCommands)
        {
            actions.Children.Add(CatalogJobCommand(entry, job, command));
        }
        content.Children.Add(actions);
        return Inner(content);
    }

    /// <summary>Every line, like PostgreSQL's majors: install next to the others, switch, update, uninstall.</summary>
    private StackPanel CatalogLines(CatalogService entry, ServiceReleaseRow row)
    {
        var rows = new List<UIElement>();
        foreach (var line in row.Lines)
        {
            var source = entry.Lines.First(candidate => candidate.Id == line.Target.Line);
            var title = Ui.Row(8, Ui.Text($"{entry.Name} {line.Target.Label}", line.IsInstalled ? "BodyStrongTextBlockStyle" : "BodyTextBlockStyle", wrap: false));
            if (line.IsActive)
            {
                title.Children.Add(Ui.Badge(Strings.Services_Active));
            }
            var info = Ui.Column(2, title, Ui.Caption(line.Facts, line.HasUpdate ? Ui.Accent : null, false));
            if (source.Yanked.Length > 0)
            {
                var withdrawn = Ui.Caption(Localization.Format(line.HasUpdate ? Strings.Services_WithdrawnUpdate : Strings.Services_Withdrawn, source.Yanked), Ui.Caution);
                ToolTipService.SetToolTip(withdrawn, Strings.Services_WithdrawnTooltip);
                info.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), withdrawn));
            }

            var actions = Ui.Row(8);
            foreach (var command in line.PrimaryCommands)
            {
                actions.Children.Add(CatalogCommandButton(entry, source, line, command));
            }
            if (line.MoreCommands.Count > 0)
            {
                var more = new MenuFlyout();
                foreach (var command in line.MoreCommands)
                {
                    switch (command)
                    {
                        case ServiceRowCommandKind.Activate:
                            more.Items.Add(Item(Strings.Services_SwitchEllipsis, () => _ = SwitchCatalog(entry, source, line.Target)));
                            break;
                        case ServiceRowCommandKind.Uninstall:
                            more.Items.Add(Item(Strings.Common_UninstallEllipsis, () => _ = UninstallCatalog(entry, source, line.Target)));
                            break;
                        case ServiceRowCommandKind.Reinstall:
                            more.Items.Add(Item(Localization.Format(Strings.Services_Reinstall, source.Version), () => _ = RunCatalogAsync(entry.Id,
                                () => Model.ReleaseActions.RunAsync(line.Target, command))));
                            break;
                        case ServiceRowCommandKind.ShowLog:
                            more.Items.Add(Item(Strings.Services_ShowLastLog, () => _ = ShowCatalogLog(entry)));
                            break;
                    }
                }
                var moreButton = Ui.IconButton(Ui.GlyphMore, Strings.Services_More, () => { });
                moreButton.Flyout = more;
                actions.Children.Add(moreButton);
            }
            rows.Add(VersionRow(info, actions));
        }
        return Ui.Column(6, Ui.Text(Strings.Versions_Versions, "BodyStrongTextBlockStyle"), VersionRows(rows),
            Ui.Caption(entry.Lines.Count > 1 ? Strings.Services_SideBySide : Strings.Services_UpdatesKeepData));
    }

    private Button CatalogCommandButton(CatalogService entry, CatalogLine source, ServiceReleaseLine line, ServiceRowCommandKind command)
    {
        return command switch
        {
            ServiceRowCommandKind.Install => Ui.Button(Strings.Common_Install, () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(line.Target, command)),
                glyph: Ui.GlyphDownload, tooltip: (source.Latest.Length > 0 ? Localization.Format(Strings.Services_Downloads, $"{entry.Name} {source.Latest}") : Strings.Services_DownloadsNewest)
                                                  + (entry.IsInstalled ? " " + Localization.Format(Strings.Services_KeepsRunning, $"{entry.Name} {entry.Line}") : "")
                                                  + (source.HasData ? " " + Strings.Services_DataComesBack : "")),
            ServiceRowCommandKind.Update => Ui.Button(Localization.Format(Strings.General_UpdateTo, source.Latest), () => _ = RunCatalogAsync(entry.Id,
                    () => Model.ReleaseActions.RunAsync(line.Target, command)), true, Ui.GlyphDownload,
                Localization.Format(source.Active ? Strings.Services_UpdateTooltipRestart : Strings.Services_UpdateTooltip, source.Latest, source.Version, entry.Name)),
            ServiceRowCommandKind.Uninstall => Ui.Button(Strings.Common_UninstallEllipsis, () => _ = UninstallCatalog(entry, source, line.Target)),
            _ => throw new ArgumentOutOfRangeException(nameof(command), command, null)
        };
    }

    private FrameworkElement CatalogJobCommand(CatalogService entry, CatalogJob job, ServiceRowCommandKind command)
    {
        var target = new ServiceReleaseTarget(entry.Id, ServiceReleaseSource.Catalog, job.Line, entry.Name, job.Version);
        return command switch
        {
            ServiceRowCommandKind.ShowLog => Ui.Link(Strings.Sites_ShowLog, () => _ = ShowCatalogLog(entry)),
            ServiceRowCommandKind.Cancel => Ui.Button(Strings.Common_Cancel, () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(target, command))),
            ServiceRowCommandKind.Retry => Ui.Button(Strings.Setup_TryAgain, () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(target, command)),
                true, Ui.GlyphRefresh),
            ServiceRowCommandKind.Dismiss => Ui.Button(Strings.Sites_Share_Dismiss, () => _ = RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(target, command))),
            _ => throw new ArgumentOutOfRangeException(nameof(command), command, null)
        };
    }

    private async Task SwitchCatalog(CatalogService entry, CatalogLine target, ServiceReleaseTarget releaseTarget)
    {
        var own = Localization.Format(target.HasData ? Strings.Services_UsesOwnData : Strings.Services_StartsEmpty, $"{entry.Name} {target.Label}");
        if (!entry.IsInstalled || await Window.Confirm(Localization.Format(Strings.Services_SwitchQuestion, $"{entry.Name} {target.Label}"),
                Localization.Format(Strings.Services_SwitchDetails, $"{entry.Name} {entry.Line}") + " " + own, Strings.Worktrees_Switch))
        {
            await RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(releaseTarget, ServiceRowCommandKind.Activate));
        }
    }

    private async Task UninstallCatalog(CatalogService entry, CatalogLine line, ServiceReleaseTarget releaseTarget)
    {
        var next = entry.Lines.FirstOrDefault(l => l.Installed && l.Id != line.Id);
        var details = !line.Active ? ""
            : next is not null ? Localization.Format(Strings.Services_UninstallNext, $"{entry.Name} {line.Label}", $"{entry.Name} {next.Label}") + " "
            : Localization.Format(Strings.Services_UninstallLast, $"{entry.Name} {line.Label}", entry.Name) + " ";
        details += line.HasData
            ? Localization.Format(Strings.Services_DataStays, line.Label)
            : Strings.Services_NoData;

        CheckBox? delete = null;
        UIElement? extra = null;
        if (line.HasData)
        {
            delete = new CheckBox { Content = Strings.Services_AlsoDeleteData };
            var warning = Ui.Caption(Strings.Services_DeletedDataGone, Ui.Caution);
            warning.Visibility = Visibility.Collapsed;
            warning.Margin = new Thickness(28, -4, 0, 0);
            delete.Checked += (_, _) => warning.Visibility = Visibility.Visible;
            delete.Unchecked += (_, _) => warning.Visibility = Visibility.Collapsed;
            extra = Ui.Column(4, delete, warning);
        }
        if (await Window.Confirm(Localization.Format(Strings.Services_UninstallQuestion, $"{entry.Name} {line.Label}"), details, Strings.Common_Uninstall, true, extra))
        {
            var deleteData = delete?.IsChecked == true;
            await RunCatalogAsync(entry.Id, () => Model.ReleaseActions.RunAsync(releaseTarget, ServiceRowCommandKind.Uninstall, deleteData));
        }
    }

    private async Task RunCatalogAsync(string id, Func<Task<OperationOutcome>> action)
    {
        Kick();
        await action();
        Kick();
    }

    private Task ShowCatalogLog(CatalogService entry)
    {
        return JobLogDialog.ShowAsync(Window, Localization.Format(Strings.Services_InstallLog, entry.Name), token => Model.Catalog.ReadLogAsync(entry.Id, token), () => CatalogWorking(entry.Id));
    }
}
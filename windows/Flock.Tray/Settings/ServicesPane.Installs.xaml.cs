using System.Globalization;
using Flock.Kit;
using Flock.Tray.Features.Services;
using Flock.Tray.Infrastructure;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     Installing, updating and switching PostgreSQL versions, and installing catalog services
///     (downloads; the daemon runs them as jobs). State that other windows or the
///     CLI start shows up too: the daemon reports PostgreSQL's busy majors and the component jobs.
/// </summary>
internal sealed partial class ServicesPane
{
    // The poll loop's wait; cancelled to look again right away after starting something.
    private CancellationTokenSource? nap;

    /// <summary>Some PostgreSQL operation runs: other PostgreSQL actions wait for it.</summary>
    private bool PostgresWorking => Model.Postgres.IsRunning || Model.Postgres.Current?.AnyBusy == true;


    private static bool IsInstallable(Service service)
    {
        return service.Id == "postgres" || service.IsOnRequest;
    }

    /// <summary>
    ///     Polls every 2 s while something installs; otherwise the store reloads when the service list
    ///     changes, and a slow poll catches jobs that the CLI started.
    /// </summary>
    private async Task PollAsync(CancellationToken hidden)
    {
        while (!hidden.IsCancellationRequested)
        {
            var fast = PostgresWorking || Model.Catalog.Current?.HasRunningJob == true;
            await (fast ? rows.RefreshReleaseStateAsync(hidden) : rows.RefreshAsync(hidden));
            using var wake = CancellationTokenSource.CreateLinkedTokenSource(hidden);
            nap = wake;
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(fast ? 2 : 30), Model.TimeProvider, wake.Token);
            }
            catch (TaskCanceledException)
            {
            }
            finally
            {
                nap = null;
            }
        }
    }
    private void Kick()
    {
        nap?.Cancel();
    }

    /// <summary>The tooltip of a service row's update hint; null when there is nothing to update.</summary>
    private string? UpdateHint(Service service)
    {
        if (service.Id == "postgres")
        {
            if (Model.Postgres.Current?.Majors.FirstOrDefault(m => m.Update) is { } major)
            {
                return Localization.Format(major.Latest == major.Installed ? Strings.Services_NewBuildAvailable : Strings.Services_VersionAvailable, $"PostgreSQL {major.Latest}");
            }
        }
        if (Model.Catalog.Current?[service.Id] is { } entry && entry.Lines.FirstOrDefault(l => l.Update) is { } line)
        {
            return Localization.Format(Strings.Services_VersionAvailable, $"{entry.Name} {line.Latest}");
        }
        return null;
    }

    /// <summary>What a service row says while something about it runs; null when idle.</summary>
    private string? WorkText(Service service)
    {
        if (service.Id == "postgres")
        {
            if (Model.Postgres.Current?.Majors.FirstOrDefault(m => m.IsBusy) is { } busy)
            {
                return busy.BusyText;
            }
        }
        if (CatalogWorking(service.Id))
        {
            return Model.Catalog.Current?[service.Id]?.Job?.PhaseText ?? Strings.Common_Working;
        }
        return null;
    }

    // PostgreSQL

    private void PostgresDetail(StackPanel panel)
    {
        if (Model.Postgres.Current is not { } versions)
        {
            panel.Children.Add(LoadingRow(Strings.Services_Postgres_Loading));
            return;
        }
        if (!versions.AnyInstalled)
        {
            panel.Children.Add(PostgresWorking ? FirstInstall(versions) : PostgresEmpty(versions, rows.ReleaseActions.InstallTargets("postgres")));
        }
        else
        {
            panel.Children.Add(ActiveCard(versions));
            panel.Children.Add(VersionList(versions, rows.ReleaseActions.ResolveRow("postgres")));
        }
        panel.Children.Add(CheckFooter("postgres", rows.ReleaseActions.Resolve("postgres")));
        panel.Children.Add(Ui.Caption(Strings.Services_Postgres_ClientTools));
    }

    /// <summary>Nothing installed yet: the version and port, and Install, as for the catalog services.</summary>
    private Border PostgresEmpty(PostgresVersions versions, IReadOnlyList<ServiceReleaseTarget> targets)
    {
        if (targets.Count == 0)
        {
            return Inner(Ui.Caption(Localization.Format(Strings.Services_NoVersions, "PostgreSQL")));
        }
        var hero = InstallCard("postgres", "PostgreSQL", Strings.Services_Postgres_Description, targets, versions.Port, null, "");
        var kept = versions.Majors.Where(m => m.HasData).Select(m => m.Major).ToList();
        if (kept.Count > 0)
        {
            var note = Ui.Caption(Localization.Format(Strings.Services_Postgres_DatabasesKept, string.Join(", ", kept)));
            note.TextAlignment = TextAlignment.Center;
            ((Panel)hero.Child).Children.Add(note);
        }
        return hero;
    }

    /// <summary>The very first install: it downloads, creates the cluster and starts it.</summary>
    private Border FirstInstall(PostgresVersions versions)
    {
        var major = versions.Majors.FirstOrDefault(m => m.IsBusy);
        var name = major is null ? "PostgreSQL" : $"PostgreSQL {(major.Latest.Length > 0 ? major.Latest : major.Major)}";
        var heading = major is { Busy: PostgresBusy.Uninstalling } ? Strings.Services_Postgres_Uninstalling : Strings.Setup_Installing;
        return Inner(Ui.Column(10,
                Ui.Text(Localization.Format(heading, name), "BodyStrongTextBlockStyle"),
                new Controls.OperationProgress { Text = Strings.Services_Postgres_CreatingCluster, IsIndeterminate = true },
                Ui.Caption(Strings.Services_Postgres_FirstInstallDetails)),
            new Thickness(16, 14, 16, 14));
    }

    /// <summary>The active version: what runs, where, and how much data it holds.</summary>
    private Border ActiveCard(PostgresVersions versions)
    {
        if (versions.ActiveMajor is not { } active)
        {
            return Inner(Ui.Caption(Strings.Services_Postgres_NoneActive));
        }
        var (state, up) = versions.Running ? (Strings.Menu_Status_Running, true) : versions.Enabled ? (Strings.Menu_Status_NotRunning, false) : (Strings.Services_Postgres_Stopped, false);
        var facts = $"{state} · " + Localization.Format(Strings.Services_PortNumber, versions.Port)
                    + (active.HasData ? " · " + Localization.Format(Strings.Services_DataSize, Ui.Bytes(active.DataSize)) : "");

        var text = Ui.Column(2, Ui.Caption(Strings.Services_Postgres_ActiveVersion, wrap: false), Ui.Text($"PostgreSQL {active.Installed}", "SubtitleTextBlockStyle", wrap: false),
            Ui.Row(6, Ui.StatusDot(up), Ui.Caption(facts)));
        if (BusyText(active) is { } busy)
        {
            text.Children.Add(Ui.Row(6, new ProgressRing { Width = 12, Height = 12 }, Ui.Caption(busy, wrap: false)));
        }
        else if (active.Update)
        {
            text.Children.Add(Ui.Caption(Localization.Format(active.Latest == active.Installed ? Strings.Services_Postgres_NewBuildUpdateBelow : Strings.Services_Postgres_UpdateBelow,
                active.Latest), Ui.Accent));
        }
        if (active.Yanked.Length > 0)
        {
            var withdrawn = Ui.Caption(Localization.Format(Strings.Services_Postgres_Withdrawn, active.Yanked), Ui.Caution);
            ToolTipService.SetToolTip(withdrawn, Strings.Services_Postgres_WithdrawnTooltip);
            text.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), withdrawn));
        }
        var icon = Ui.Icon(Ui.GlyphServer, 26, up ? Ui.Accent : Ui.TertiaryText);
        icon.VerticalAlignment = VerticalAlignment.Top;
        icon.Margin = new Thickness(0, 4, 0, 0);
        return Inner(Ui.Row(14, icon, text), new Thickness(16, 14, 16, 14));
    }

    private StackPanel VersionList(PostgresVersions versions, ServiceReleaseRow row)
    {
        return Ui.Column(6, Ui.Text(Strings.Versions_Versions, "BodyStrongTextBlockStyle"),
            VersionRows(versions.Majors.Zip(row.Lines).Select(pair => MajorRow(versions, pair.First, pair.Second))),
            Ui.Caption(Strings.Services_Postgres_SideBySide));
    }

    private string? BusyText(PostgresMajor major)
    {
        return major.IsBusy ? major.BusyText : null;
    }

    /// <summary>A major with its actions on the right, laid out like a catalog service's line.</summary>
    private Grid MajorRow(PostgresVersions versions, PostgresMajor major, ServiceReleaseLine line)
    {
        var title = Ui.Row(8, Ui.Text($"PostgreSQL {line.Target.Label}", line.IsInstalled ? "BodyStrongTextBlockStyle" : "BodyTextBlockStyle", wrap: false));
        if (line.IsActive)
        {
            title.Children.Add(Ui.Badge(Strings.Services_Active));
        }
        var info = Ui.Column(2, title, Ui.Caption(line.Facts, line.HasUpdate ? Ui.Accent : null));
        var actions = Ui.Row(8);
        if (line.BusyText is { } busy)
        {
            actions.Children.Add(new ProgressRing { Width = 14, Height = 14 });
            actions.Children.Add(Ui.Caption(busy, wrap: false));
        }
        else
        {
            foreach (var command in line.PrimaryCommands)
            {
                actions.Children.Add(PostgresCommandButton(versions, major, line, command));
            }
            if (line.MoreCommands.Count > 0)
            {
                actions.Children.Add(More(versions, major, line));
            }
        }
        // One PostgreSQL operation at a time.
        Ui.SetEnabled(actions, !PostgresWorking);
        return VersionRow(info, actions);
    }

    private Button PostgresCommandButton(PostgresVersions versions, PostgresMajor major, ServiceReleaseLine line, ServiceRowCommandKind command)
    {
        return command switch
        {
            ServiceRowCommandKind.Install => Ui.Button(Strings.Common_Install, () => _ = RunAsync(() => rows.ReleaseActions.RunAsync(line.Target, command)), glyph: Ui.GlyphDownload,
                tooltip: (major.Latest.Length > 0 ? Localization.Format(Strings.Services_Downloads, $"PostgreSQL {major.Latest}") : Strings.Services_DownloadsNewest)
                         + (versions.Active.Length > 0 ? " " + Localization.Format(Strings.Services_Postgres_StaysActive, $"PostgreSQL {versions.Active}") : "")
                         + (major.HasData ? " " + Strings.Services_Postgres_DatabasesComeBack : "")),
            ServiceRowCommandKind.Update => Ui.Button(Localization.Format(Strings.General_UpdateTo, major.Latest), () => _ = RunAsync(() => rows.ReleaseActions.RunAsync(line.Target, command)), true,
                Ui.GlyphDownload,
                Localization.Format(major.Active && versions.Running ? Strings.Services_Postgres_UpdateTooltipRestart : Strings.Services_Postgres_UpdateTooltip, major.Latest, major.Installed)),
            ServiceRowCommandKind.Uninstall => Ui.Button(Strings.Common_UninstallEllipsis, () => _ = UninstallAsync(versions, major, line.Target)),
            _ => throw new ArgumentOutOfRangeException(nameof(command), command, null)
        };
    }

    private Button More(PostgresVersions versions, PostgresMajor major, ServiceReleaseLine line)
    {
        var menu = new MenuFlyout();
        foreach (var command in line.MoreCommands)
        {
            switch (command)
            {
                case ServiceRowCommandKind.Uninstall:
                    menu.Items.Add(Item(Strings.Common_UninstallEllipsis, () => _ = UninstallAsync(versions, major, line.Target)));
                    break;
                case ServiceRowCommandKind.Activate:
                    menu.Items.Add(Item(Strings.Services_SwitchEllipsis, () => _ = SwitchAsync(versions, major, line.Target)));
                    break;
            }
        }
        var button = Ui.IconButton(Ui.GlyphMore, Strings.Services_More, () => { });
        button.Flyout = menu;
        button.IsEnabled = !PostgresWorking;
        return button;
    }

    private async Task SwitchAsync(PostgresVersions versions, PostgresMajor major, ServiceReleaseTarget target)
    {
        if (versions.Active.Length > 0 && !await Window.Confirm(Localization.Format(Strings.Services_SwitchQuestion, $"PostgreSQL {major.Major}"),
                Localization.Format(Strings.Services_Postgres_SwitchDetails, versions.Active, major.Major) + (major.HasData ? "" : " " + Strings.Services_Postgres_StartsEmpty),
                Strings.Worktrees_Switch))
        {
            return;
        }
        await RunAsync(() => rows.ReleaseActions.RunAsync(target, ServiceRowCommandKind.Activate));
    }

    private async Task UninstallAsync(PostgresVersions versions, PostgresMajor major, ServiceReleaseTarget target)
    {
        var next = versions.Majors.FirstOrDefault(m => m.IsInstalled && m.Major != major.Major);
        var details = !major.Active ? ""
            : next is not null ? Localization.Format(Strings.Services_UninstallNext, $"PostgreSQL {major.Major}", $"PostgreSQL {next.Major}") + " "
            : Localization.Format(Strings.Services_Postgres_UninstallLast, major.Major) + " ";
        details += major.HasData
            ? Localization.Format(Strings.Services_Postgres_DatabasesStay, major.Major)
            : Strings.Services_Postgres_NoDatabases;

        CheckBox? delete = null;
        UIElement? extra = null;
        if (major.HasData)
        {
            delete = new CheckBox { Content = Localization.Format(Strings.Services_Postgres_AlsoDeleteDatabases, Ui.Bytes(major.DataSize)) };
            var warning = Ui.Caption(Strings.Services_Postgres_DeletedDatabasesGone, Ui.Caution);
            warning.Visibility = Visibility.Collapsed;
            warning.Margin = new Thickness(28, -4, 0, 0);
            delete.Checked += (_, _) => warning.Visibility = Visibility.Visible;
            delete.Unchecked += (_, _) => warning.Visibility = Visibility.Collapsed;
            extra = Ui.Column(4, delete, warning);
        }
        if (await Window.Confirm(Localization.Format(Strings.Services_UninstallQuestion, $"PostgreSQL {major.Major}"), details, Strings.Common_Uninstall, true, extra))
        {
            var deleteData = delete?.IsChecked == true;
            await RunAsync(() => rows.ReleaseActions.RunAsync(target, ServiceRowCommandKind.Uninstall, deleteData));
        }
    }

    private static async Task RunAsync(Func<Task<OperationOutcome>> action)
    {
        await action();
    }

    // Shared pieces

    /// <summary>"Last checked …" with "Check for Updates", and the last check's error, kept small.</summary>
    private StackPanel CheckFooter(string serviceId, ServiceReleaseAction action)
    {
        var text = action.CheckedAt is { } at ? Localization.Format(Strings.Versions_LastChecked, Ui.Relative(at)) : Strings.Versions_NotChecked;
        UIElement release = action.Kind switch
        {
            ServiceReleaseActionKind.Check => Ui.Link(action.AccessibleLabel, () => _ = RunReleaseActionAsync(serviceId, action)),
            ServiceReleaseActionKind.Install or ServiceReleaseActionKind.Update or ServiceReleaseActionKind.UpdateAll => Ui.Button(action.AccessibleLabel,
                () => _ = RunReleaseActionAsync(serviceId, action), true, Ui.GlyphDownload),
            ServiceReleaseActionKind.Busy => Ui.Row(6, new ProgressRing { Width = 14, Height = 14 }, Ui.Caption(action.BusyText ?? action.AccessibleLabel, wrap: false)),
            ServiceReleaseActionKind.Loading => new ProgressRing { Width = 14, Height = 14 },
            _ => Ui.Caption(action.AccessibleLabel, wrap: false)
        };
        if (!action.IsEnabled && release is Control control)
        {
            control.IsEnabled = false;
        }
        var footer = Ui.Column(2, Ui.Spread(Ui.Caption(text, wrap: false), release));
        if (action.CheckError.Length > 0)
        {
            var error = Ui.Caption(Localization.Format(Strings.Versions_CouldNotCheck, action.CheckError), Ui.Caution);
            error.MaxLines = 2;
            ToolTipService.SetToolTip(error, action.CheckError);
            footer.Children.Add(Ui.Row(6, Ui.Icon(Ui.GlyphWarning, 12, Ui.Caution), error));
        }
        return footer;
    }

    private async Task RunReleaseActionAsync(string serviceId, ServiceReleaseAction action)
    {
        Kick();
        if (action.Kind == ServiceReleaseActionKind.Check)
        {
            await rows.ReleaseActions.CheckAsync(serviceId);
            return;
        }
        if (action.Kind == ServiceReleaseActionKind.Update && action.Targets is [var target])
        {
            await rows.ReleaseActions.RunTargetAsync(target);
            return;
        }
        if (action.Kind == ServiceReleaseActionKind.UpdateAll)
        {
            var serviceName = serviceId == "postgres" ? "PostgreSQL" : Model.Catalog.Current?[serviceId]?.Name ?? serviceId;
            var details = string.Join(Environment.NewLine, action.Targets.Select(target => $"{target.Label} → {target.Latest}"));
            if (await Window.Confirm(Localization.Plural(Strings.ResourceManager, "Services_InstallUpdatesQuestion", action.Targets.Count, serviceName), details,
                    Strings.Common_Install))
            {
                await rows.ReleaseActions.RunAllUpdatesAsync(serviceId);
            }
            return;
        }
        if (action.Kind != ServiceReleaseActionKind.Install)
        {
            return;
        }
        var targets = rows.ReleaseActions.InstallTargets(serviceId);
        if (serviceId == "postgres" && Model.Postgres.Current is { } versions)
        {
            var line = targets.Any(target => target.Line == rows.PostgresDraftMajor)
                ? rows.PostgresDraftMajor
                : targets.FirstOrDefault()?.Line;
            if (line is not null)
            {
                rows.PostgresDraftMajor = line;
                await rows.ReleaseActions.InstallFirstAsync(serviceId, line, rows.PostgresDraftPort, versions.Port);
            }
        }
        else if (Model.Catalog.Current?[serviceId] is { } entry)
        {
            var draft = rows.CatalogServices.FirstOrDefault(service => service.Id == serviceId);
            var line = targets.Any(target => target.Line == draft?.DraftLine)
                ? draft?.DraftLine
                : targets.FirstOrDefault()?.Line;
            if (line is not null)
            {
                draft!.DraftLine = line;
                await rows.ReleaseActions.InstallFirstAsync(serviceId, line, draft?.DraftPort ?? entry.Port, entry.Port);
            }
        }
    }

    /// <summary>
    ///     Installing a service that isn't installed yet, the same for PostgreSQL and the catalog
    ///     services: the version (when there is a choice) and the port, then Install.
    /// </summary>
    private Border InstallCard(string id, string name, string description, IReadOnlyList<ServiceReleaseTarget> targets, int port, string? portNote, string estimate)
    {
        var form = Ui.Column(10);
        var picker = new ComboBox { Header = Strings.General_Version, MinWidth = 220 };
        foreach (var target in targets)
        {
            picker.Items.Add(new ComboBoxItem { Content = target.Latest.Length > 0 ? $"{target.Label} ({target.Latest})" : target.Label, Tag = target.Line });
        }
        var catalogDraft = rows.CatalogServices.FirstOrDefault(service => service.Id == id);
        var picked = id == "postgres" ? rows.PostgresDraftMajor : catalogDraft?.DraftLine;
        picker.SelectedItem = picker.Items.OfType<ComboBoxItem>().FirstOrDefault(i => (string)i.Tag == picked) ?? picker.Items.FirstOrDefault();
        picker.SelectionChanged += (_, _) =>
        {
            if (picker.SelectedItem is ComboBoxItem { Tag: string version })
            {
                if (id == "postgres")
                {
                    rows.PostgresDraftMajor = version;
                }
                else if (catalogDraft is not null)
                {
                    catalogDraft.DraftLine = version;
                }
            }
        };
        // With one version there's nothing to pick, but the box still says what installs.
        picker.IsEnabled = targets.Count > 1;
        form.Children.Add(picker);

        var portBox = new NumberBox
        {
            Header = Strings.Mail_Port,
            Value = id == "postgres" ? rows.PostgresDraftPort : catalogDraft?.DraftPort ?? port,
            Minimum = 1024,
            Maximum = 65535,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
            MinWidth = 140,
            HorizontalAlignment = HorizontalAlignment.Left
        };
        portBox.ValueChanged += (_, _) =>
        {
            if (double.IsNaN(portBox.Value))
            {
                return;
            }
            if (id == "postgres")
            {
                rows.PostgresDraftPort = (int)portBox.Value;
            }
            else if (catalogDraft is not null)
            {
                catalogDraft.DraftPort = (int)portBox.Value;
            }
        };
        form.Children.Add(portBox);
        if (portNote is not null)
        {
            form.Children.Add(Ui.Caption(portNote));
        }

        UIElement[] actions = estimate.Length > 0 ? [form, Ui.Caption(Capitalize(estimate), wrap: false)] : [form];
        return Hero(Ui.GlyphServer, Localization.Format(Strings.Services_InstallName, name), description, actions);
    }

    /// <summary>Version rows in one surface, divided like the macOS list.</summary>
    private static Border VersionRows(IEnumerable<UIElement> rows)
    {
        var list = new StackPanel();
        foreach (var row in rows)
        {
            if (list.Children.Count > 0)
            {
                list.Children.Add(new Border { Height = 1, Background = Ui.Brush("DividerStrokeColorDefaultBrush") });
            }
            list.Children.Add(row);
        }
        return Inner(list, new Thickness(14, 2, 8, 2));
    }

    /// <summary>One version: what it is on the left, its buttons on the right.</summary>
    private static Grid VersionRow(FrameworkElement info, FrameworkElement actions)
    {
        var row = new Grid { ColumnSpacing = 8, Padding = new Thickness(0, 10, 0, 10) };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        info.VerticalAlignment = VerticalAlignment.Center;
        actions.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(actions, 1);
        row.Children.Add(info);
        row.Children.Add(actions);
        return row;
    }

    /// <summary>A surface inside the detail card.</summary>
    private static Border Inner(UIElement content, Thickness? padding = null)
    {
        return new Border
        {
            Child = content,
            Padding = padding ?? new Thickness(14, 12, 14, 12),
            Background = Ui.Brush("CardBackgroundFillColorSecondaryBrush"),
            BorderBrush = Ui.Brush("CardStrokeColorDefaultBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(6)
        };
    }

    /// <summary>A centered call to action for something not installed yet.</summary>
    private static Border Hero(string glyph, string title, string description, params UIElement[] actions)
    {
        var panel = Ui.Column(8, Ui.Icon(glyph, 32, Ui.Accent), Ui.Text(title, "BodyStrongTextBlockStyle"));
        var caption = Ui.Caption(description);
        caption.TextAlignment = TextAlignment.Center;
        panel.Children.Add(caption);
        for (var i = 0; i < actions.Length; i++)
        {
            if (actions[i] is FrameworkElement action)
            {
                action.Margin = new Thickness(0, i == 0 ? 6 : 0, 0, 0);
            }
            panel.Children.Add(actions[i]);
        }
        foreach (var child in panel.Children.OfType<FrameworkElement>())
        {
            child.HorizontalAlignment = HorizontalAlignment.Center;
        }
        return Inner(panel, new Thickness(20, 20, 20, 18));
    }

    private static StackPanel LoadingRow(string text)
    {
        return Ui.Row(10, new ProgressRing { Width = 16, Height = 16 }, Ui.Caption(text, wrap: false));
    }

    private static MenuFlyoutItem Item(string text, Action click)
    {
        var item = new MenuFlyoutItem { Text = text };
        item.Click += (_, _) => click();
        return item;
    }

    private static string Capitalize(string text)
    {
        return text.Length == 0 ? text : char.ToUpper(text[0], CultureInfo.CurrentCulture) + text[1..];
    }
}
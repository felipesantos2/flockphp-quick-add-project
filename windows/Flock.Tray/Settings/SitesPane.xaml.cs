using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     The sites in a list, and the selected one's details beside it, as in Services: its address,
///     domains and document root, its PHP and Node.js versions and what serves it (see
///     <see cref="SiteDetailViewModel" />). Each site runs on php-cgi or on FrankenPHP (classic, or
///     Laravel Octane's worker mode). Proxies (`flock proxy`) follow the sites in a group of their own.
///     The details are XAML views over view models; the pane runs their actions and dialogs.
/// </summary>
internal sealed partial class SitesPane : UserControl, ISettingsPane, ISiteDetailHost
{
    private readonly RuntimeService Model;
    private readonly SettingsWindow Window;
    private readonly Dictionary<string, Task> actions = [];
    private readonly DeepLinkCoordinator deepLinks;
    private readonly bool loaded;

    // Sites whose FrankenPHP (or dev server) restart is in flight.
    private readonly HashSet<string> restarting = [];
    private readonly SitesViewModel viewModel;
    private readonly SiteDetailViewModel siteDetail;
    private readonly WorktreeDetailViewModel worktreeDetail;
    private readonly ProxyDetailViewModel proxyDetail;
    // The output of the selected Node.js site's dev server, site's Reverb or proxy's command.
    private readonly OutputViewModel output;
    private bool active;
    private CancellationTokenSource? activeLifetime;
    private Task pendingSecure = Task.CompletedTask;

    public SitesPane(SettingsWindow window, RuntimeService model, DeepLinkCoordinator deepLinks)
    {
        Window = window;
        Model = model;
        viewModel = new SitesViewModel(model.Sites);
        output = new OutputViewModel(model.Sites, log => App.Current.OpenLog(new LogsViewModel.ServiceSource(log)));
        siteDetail = new SiteDetailViewModel(this, output);
        worktreeDetail = new WorktreeDetailViewModel(this, output);
        proxyDetail = new ProxyDetailViewModel(this, output);
        this.deepLinks = deepLinks;
        InitializeComponent();
        SiteDetails.DataContext = siteDetail;
        WorktreeDetails.DataContext = worktreeDetail;
        ProxyDetails.DataContext = proxyDetail;
        DataContext = viewModel;
        loaded = true;
        Model.Changed += StateChanged;
        viewModel.SelectionChanged += RefreshDetail;
        RefreshDetail();
    }

    public SitesPane(SettingsWindow window, RuntimeService model) : this(window, model, new DeepLinkCoordinator(model))
    {
    }

    public string Title => Strings.Settings_Pane_Sites;
    public string Subtitle => Strings.Sites_Subtitle;
    public UIElement Root => this;
    public bool ScrollsItself => true;

    public async Task ActivateAsync(CancellationToken hidden)
    {
        active = true;
        activeLifetime?.Dispose();
        activeLifetime = CancellationTokenSource.CreateLinkedTokenSource(hidden);
        ApplyStatus();
        RefreshDetail(); // the output reads again
        await HandlePendingSecureAsync(hidden);
    }

    public Task ReleaseAsync()
    {
        active = false;
        output.Stop();
        activeLifetime?.Cancel();
        Model.Changed -= StateChanged;
        viewModel.SelectionChanged -= RefreshDetail;
        return DrainAsync();
    }

    public void Hidden()
    {
        active = false;
        output.Stop();
        activeLifetime?.Cancel();
    }

    private void StateChanged()
    {
        ApplyStatus();
        if (active && Model.PendingSecure is not null && pendingSecure.IsCompleted)
        {
            pendingSecure = ObservePendingSecureAsync(activeLifetime?.Token ?? CancellationToken.None);
        }
    }

    private async Task ObservePendingSecureAsync(CancellationToken cancellationToken)
    {
        try
        {
            await HandlePendingSecureAsync(cancellationToken);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    private void Start(string resource, Func<Task> action, bool refreshAfter = true)
    {
        if (actions.TryGetValue(resource, out var running) && !running.IsCompleted)
        {
            return;
        }
        actions[resource] = ObserveActionAsync(resource, action, refreshAfter);
    }

    private async Task ObserveActionAsync(string resource, Func<Task> action, bool refreshAfter)
    {
        try
        {
            await action();
        }
        catch (OperationCanceledException) when (activeLifetime?.IsCancellationRequested == true)
        {
        }
        catch (Exception error)
        {
            App.Log(error);
        }
        finally
        {
            actions.Remove(resource);
            if (refreshAfter)
            {
                RefreshDetail();
            }
        }
    }

    private async Task UpdateSiteAsync(string site, SiteChange change, Func<Action?>? setDraft)
    {
        var rollback = setDraft?.Invoke();
        try
        {
            var outcome = await Model.Sites.UpdateAsync(site, change);
            if (outcome == OperationOutcome.Succeeded)
            {
                return;
            }
            rollback?.Invoke();
            if (outcome == OperationOutcome.Failed)
            {
                viewModel.Apply(Model.Status);
            }
            RefreshDetail();
        }
        catch
        {
            rollback?.Invoke();
            RefreshDetail();
            throw;
        }
    }

    private async Task DrainAsync()
    {
        var tasks = actions.Values.Append(pendingSecure).ToArray();
        if (tasks.Length > 0)
        {
            await Task.WhenAll(tasks);
        }
        activeLifetime?.Dispose();
        activeLifetime = null;
    }

    private async Task HandlePendingSecureAsync(CancellationToken hidden)
    {
        var resolution = await deepLinks.ConsumeSecureAsync(hidden);
        if (resolution is null)
        {
            return;
        }
        if (resolution.Site is { } site)
        {
            viewModel.Select(site);
        }
        if (resolution.Site is null)
        {
            await Ui.Inform(Window.XamlRoot, resolution.Title, resolution.Message);
            return;
        }
        if (await Window.Confirm(resolution.Title, resolution.Message, Strings.Sites_Secure))
        {
            await Model.Sites.UpdateAsync(resolution.Site, new SiteChange(Secured: true), hidden);
        }
    }

    private async Task AddSiteAsync()
    {
        if (await Window.PickFolder() is { } path)
        {
            await Model.Sites.LinkAsync(path);
        }
    }

    private async Task RemoveSiteAsync(Status.Site site)
    {
        if (await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, site.Name), Localization.Format(Strings.Sites_RemoveSiteDetails, site.Domain), Strings.Common_Remove, true))
        {
            await Model.Sites.UnlinkAsync(site.Name);
        }
    }

    internal void Select(string name)
    {
        var previous = viewModel.Selected;
        var previousProxy = viewModel.SelectedProxy;
        // Before the pane was shown (a notification opening it), its rows aren't there yet.
        viewModel.Apply(Model.Status);
        viewModel.Select(name);
        if (!ReferenceEquals(previous, viewModel.Selected) || !ReferenceEquals(previousProxy, viewModel.SelectedProxy))
        {
            DetailScroller.ChangeView(null, 0, null, true);
        }
    }

    private void ApplyStatus()
    {
        var selectedBefore = viewModel.Selected;
        var proxyBefore = viewModel.SelectedProxy;
        if (viewModel.Apply(Model.Status) && ReferenceEquals(selectedBefore, viewModel.Selected) && ReferenceEquals(proxyBefore, viewModel.SelectedProxy))
        {
            RefreshDetail();
        }
    }

    /// <summary>Shows the selection's details again, and reads the output they show (or stops reading).</summary>
    private void RefreshDetail()
    {
        if (ShowDetail() is { } shown)
        {
            output.Show(shown.Log, shown.Live && active);
        }
        else
        {
            output.Stop();
        }
    }

    /// <summary>Shows the selected site's, worktree's or proxy's details; the output they show, if any.</summary>
    private OutputSource? ShowDetail()
    {
        if (!loaded)
        {
            return null;
        }
        // A proxy: the site list has no selection, and the other way round.
        if (viewModel.SelectedProxy is { } proxy)
        {
            SiteList.SelectedItem = null;
            if (!ReferenceEquals(ProxyList.SelectedItem, proxy))
            {
                ProxyList.SelectedItem = proxy;
            }
            siteDetail.Hide();
            worktreeDetail.Hide();
            proxyDetail.Update(proxy.Snapshot);
            return proxyDetail.Output;
        }
        proxyDetail.Hide();
        // No sites: the pane shows its empty state instead (HasSites).
        if (viewModel.Selected is not { } row)
        {
            siteDetail.Hide();
            worktreeDetail.Hide();
            return null;
        }
        ProxyList.SelectedItem = null;
        if (!ReferenceEquals(SiteList.SelectedItem, row))
        {
            SiteList.SelectedItem = row;
        }
        if (row.Snapshot.IsWorktree)
        {
            siteDetail.Hide();
            worktreeDetail.Update(row.Snapshot, Model.Status);
            return worktreeDetail.Output;
        }
        worktreeDetail.Hide();
        siteDetail.Update(row.Snapshot, row, Model.Configuration.Current, Model.Status, restarting.Contains(row.Name), restartingReverb.Contains(row.Name));
        return siteDetail.Output;
    }

    private void AddSiteClicked(object sender, RoutedEventArgs args)
    {
        Start("add-site", AddSiteAsync, false);
    }

    private void NewAppClicked(object sender, RoutedEventArgs args)
    {
        Start("create-app", NewAppAsync, false);
    }

    /// <summary>Asks for a name, template and parent folder, then creates and links the project.</summary>
    private async Task NewAppAsync()
    {
        var token = activeLifetime?.Token ?? CancellationToken.None;
        List<AppTemplate> templates;
        try
        {
            templates = await Model.Sites.AppTemplatesAsync(token);
        }
        catch (OperationCanceledException) when (token.IsCancellationRequested)
        {
            return;
        }
        catch (Exception error)
        {
            if (token.IsCancellationRequested)
            {
                return;
            }
            App.Log(error);
            await new ContentDialog
            {
                Title = Strings.Sites_NewAppLoadError,
                Content = error.Message,
                CloseButtonText = Strings.Common_Ok,
                DefaultButton = ContentDialogButton.Close,
                XamlRoot = Window.XamlRoot
            }.ShowAsync();
            return;
        }
        if (token.IsCancellationRequested)
        {
            return;
        }
        var tld = Model.Status?.Dns.Tld is { Length: > 0 } t ? t : "test";
        var hasRunningDb = Model.Status?.Extras.Any(s => s.Id is "mysql" or "mariadb" && s.Up) ?? false;

        var nameBox = new TextBox { Header = Strings.Sites_NewAppName, PlaceholderText = "my-app" };
        var templateBox = new ComboBox
        {
            Header = Strings.Sites_NewAppTemplate,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            ItemsSource = templates.Select(tmpl => new ComboBoxItem { Content = tmpl.Name, Tag = tmpl }).ToList()
        };
        templateBox.SelectedIndex = Math.Max(0, templates.FindIndex(tmpl => string.Equals(tmpl.Name, "php", StringComparison.OrdinalIgnoreCase)));

        var defaultFolder = Model.Configuration.Current?.Parked.FirstOrDefault()
            ?? Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        var folderBox = new TextBox { Text = defaultFolder, Width = 280 };
        var browseButton = new Button { Content = Strings.Sites_NewAppBrowse };
        var folderGroup = Ui.Column(4, Ui.Text(Strings.Sites_NewAppFolder), Ui.Row(8, folderBox, browseButton));

        var dbCheck = new CheckBox { Content = Strings.Sites_NewAppDb };
        var dbServiceBox = new ComboBox
        {
            HorizontalAlignment = HorizontalAlignment.Stretch,
            Visibility = Visibility.Collapsed,
            ItemsSource = new[]
            {
                new ComboBoxItem { Content = Strings.Sites_NewAppDbServiceAuto, Tag = "" },
                new ComboBoxItem { Content = "MySQL", Tag = "mysql" },
                new ComboBoxItem { Content = "MariaDB", Tag = "mariadb" }
            },
            SelectedIndex = 0
        };
        var dbHint = Ui.Caption("", Ui.Caution);
        dbHint.Visibility = Visibility.Collapsed;

        var securedCheck = new CheckBox { Content = Strings.Sites_NewAppHttps };
        var preview = Ui.Caption("");

        var form = Ui.Column(12, nameBox, templateBox, folderGroup, dbCheck, dbServiceBox, dbHint, securedCheck, preview);
        var scroller = new ScrollViewer
        {
            Content = form,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        };
        Ui.FitDialog(Window.XamlRoot, scroller);

        var dialog = new ContentDialog
        {
            Title = Strings.Sites_NewAppTitle,
            Content = scroller,
            PrimaryButtonText = Strings.Sites_NewAppCreate,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            IsPrimaryButtonEnabled = false,
            XamlRoot = Window.XamlRoot
        };

        AppTemplate SelectedTemplate() =>
            (templateBox.SelectedItem as ComboBoxItem)?.Tag as AppTemplate ?? new AppTemplate { Name = "php" };

        void SyncState()
        {
            var tmpl = SelectedTemplate();
            if (tmpl.RequiresDb)
            {
                dbCheck.IsChecked = true;
            }
            dbCheck.IsEnabled = !tmpl.RequiresDb;

            var wantsDb = dbCheck.IsChecked == true;
            dbServiceBox.Visibility = wantsDb ? Visibility.Visible : Visibility.Collapsed;
            dbHint.Text = tmpl.RequiresDb
                ? Strings.Sites_NewAppHintRequiresDb
                : Strings.Sites_NewAppHintNoDb;
            dbHint.Visibility = wantsDb && !hasRunningDb ? Visibility.Visible : Visibility.Collapsed;

            var rawName = nameBox.Text.Trim();
            var rawFolder = folderBox.Text.Trim();
            var slug = AppSlug(rawName);
            dialog.IsPrimaryButtonEnabled = slug.Length > 0 && rawFolder.Length > 0;

            var scheme = securedCheck.IsChecked == true ? "https" : "http";
            preview.Text = dialog.IsPrimaryButtonEnabled
                ? $"{Path.Combine(rawFolder, rawName)} → {scheme}://{slug}.{tld}"
                : Localization.Format(Strings.Sites_NewAppPreviewHint, scheme, tld);
        }

        nameBox.TextChanged += (_, _) => SyncState();
        folderBox.TextChanged += (_, _) => SyncState();
        templateBox.SelectionChanged += (_, _) => SyncState();
        foreach (var box in new[] { dbCheck, securedCheck })
        {
            box.Checked += (_, _) => SyncState();
            box.Unchecked += (_, _) => SyncState();
        }
        browseButton.Click += (_, _) => _ = PickFolderIntoAsync(folderBox);
        SyncState();

        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }

        var appName = nameBox.Text.Trim();
        var parentDir = folderBox.Text.Trim();
        var selected = SelectedTemplate();
        var createDb = dbCheck.IsChecked == true;
        var dbService = createDb && (dbServiceBox.SelectedItem as ComboBoxItem)?.Tag is string s && s.Length > 0 ? s : null;
        var secured = securedCheck.IsChecked == true;

        SetNewAppBusy(true);
        try
        {
            var outcome = await Model.Sites.CreateAppAsync(appName, parentDir, selected.Name, createDb, dbService, secured);
            if (outcome == OperationOutcome.Succeeded)
            {
                var targetPath = Path.Combine(parentDir, appName);
                var created = Model.Status?.Sites.FirstOrDefault(site => string.Equals(site.Path, targetPath, StringComparison.OrdinalIgnoreCase));
                Select(created?.Name ?? AppSlug(appName));
            }
        }
        finally
        {
            SetNewAppBusy(false);
        }
    }

    /// <summary>Hostname label matching <c>config.SiteName</c> on the daemon.</summary>
    internal static string AppSlug(string name)
    {
        var builder = new System.Text.StringBuilder(name.Length);
        foreach (var rune in name.ToLowerInvariant().EnumerateRunes())
        {
            var c = rune.Value is (>= 'a' and <= 'z') or (>= '0' and <= '9') or '-' ? (char)rune.Value : '-';
            builder.Append(c);
        }
        return builder.ToString().Trim('-');
    }

    private void SetNewAppBusy(bool busy)
    {
        NewAppButton.IsEnabled = !busy;
        EmptyNewAppButton.IsEnabled = !busy;
        NewAppProgress.IsActive = busy;
        NewAppProgress.Visibility = busy ? Visibility.Visible : Visibility.Collapsed;
        EmptyNewAppProgress.IsActive = busy;
        EmptyNewAppProgress.Visibility = busy ? Visibility.Visible : Visibility.Collapsed;
    }

    private async Task PickFolderIntoAsync(TextBox target)
    {
        try
        {
            if (await Window.PickFolder() is { } picked)
            {
                target.Text = picked;
            }
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    private void SiteSelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (SiteList.SelectedItem is SiteRowViewModel row)
        {
            Select(row.Name);
        }
    }

    private void ProxySelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (ProxyList.SelectedItem is ProxyRowViewModel row)
        {
            Select(row.Name);
        }
    }

    private void AddProxyClicked(object sender, RoutedEventArgs args)
    {
        Start("add-proxy", AddProxyAsync, false);
    }

    /// <summary>Asks for a name and an upstream, and serves &lt;name&gt;.test from it.</summary>
    private async Task AddProxyAsync()
    {
        var tld = Model.Status?.Dns.Tld is { Length: > 0 } t ? t : "test";
        var form = new AddProxyForm(Window.XamlRoot, tld, Window.PickFolder);
        var dialog = new ContentDialog
        {
            Title = Strings.Sites_AddProxyTitle,
            Content = form,
            PrimaryButtonText = Strings.Common_Add,
            CloseButtonText = Strings.Common_Cancel,
            DefaultButton = ContentDialogButton.Primary,
            XamlRoot = Window.XamlRoot
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        var proxyName = form.ProxyName;
        var proxyUpstream = form.Upstream;
        if (proxyName.Length == 0 || proxyUpstream.Length == 0)
        {
            return;
        }
        var change = form.CommandFields.Change;
        if (await Model.Sites.ProxyAsync(proxyName, proxyUpstream, change.Command is { Length: > 0 } ? change : null) == OperationOutcome.Succeeded)
        {
            Select(proxyName);
        }
    }

    private async Task ChangeUpstreamAsync(Status.Proxy proxy)
    {
        if (await Window.Ask(Localization.Format(Strings.Sites_ChangeUpstreamTitle, proxy.Name),
                Strings.Sites_ChangeUpstreamDetails,
                Strings.Sites_UpstreamPlaceholder, Strings.Sites_ChangeAction, proxy.UpstreamLabel) is { } upstream && upstream != proxy.UpstreamLabel)
        {
            await Model.Sites.ProxyAsync(proxy.Name, upstream);
        }
    }

    private async Task RemoveProxyAsync(Status.Proxy proxy)
    {
        if (await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, proxy.Name), Localization.Format(Strings.Sites_RemoveProxyDetails, proxy.Domain, proxy.Upstream), Strings.Common_Remove, true))
        {
            await Model.Sites.RemoveProxyAsync(proxy.Name);
        }
    }

    private async Task Restart(string site)
    {
        restarting.Add(site);
        RefreshDetail();
        try
        {
            await Model.Sites.RestartAsync(site);
        }
        finally
        {
            restarting.Remove(site);
            RefreshDetail();
        }
    }

    /// <summary>Asks for a folder in the project and serves the site from it on this machine.</summary>
    private async Task ChooseRoot(Status.Site site)
    {
        if (await Window.PickFolder() is not { } folder)
        {
            return;
        }
        if (site.RootFor(folder) is not { } root)
        {
            await Ui.Inform(Window.XamlRoot, Strings.Sites_ChooseFolderInProject,
                Localization.Format(Strings.Sites_ChooseFolderInProjectDetails, folder, site.Path));
            return;
        }
        await Model.Sites.UpdateAsync(site.Name, new SiteChange(Root: root));
    }

    private async Task AddBoostGuideline(Status.Site site, bool missing)
    {
        var title = Localization.Format(missing ? Strings.Sites_BoostAddQuestion : Strings.Sites_BoostUpdateQuestion, site.Name);
        var details = Localization.Format(Strings.Sites_BoostDetails, site.Path);
        if (await Window.Confirm(title, details, missing ? Strings.Common_Add : Strings.Common_Update))
        {
            await Model.Sites.AddBoostGuidelineAsync(site.Name);
        }
    }

    /// <summary>
    ///     A site's PHP extensions: what composer.json and the locked packages require, what a Laravel
    ///     app's configuration shows it uses without requiring it, and whether Flock's PHP has each.
    ///     Adding one to composer.json is confirmed inline (only one dialog can be open at a time).
    /// </summary>
    private async Task ShowExtensions(Status.Site site)
    {
        var token = activeLifetime?.Token ?? CancellationToken.None;
        // Its own key: the dialog runs under "extensions:<site>" until it's closed.
        var extensions = new SiteExtensionsViewModel(Model.Sites, site, add => Start("extensions-add:" + site.Name, add), token);
        var dialog = new ContentDialog
        {
            Title = Localization.Format(Strings.Sites_ExtensionsTitle, site.Name),
            Content = new SiteExtensionsView(extensions),
            CloseButtonText = Strings.Common_Done,
            DefaultButton = ContentDialogButton.Close,
            XamlRoot = Window.XamlRoot
        };
        dialog.Resources["ContentDialogMaxWidth"] = 640.0;
        var load = extensions.LoadAsync();
        var shown = dialog.ShowAsync().AsTask();
        if (!await load)
        {
            dialog.Hide();
        }
        await shown;
    }

    internal async Task AddDomain(Status.Site site)
    {
        var dns = Model.Status?.Dns;
        var depth = dns?.MaxDepth ?? 4;
        var alias = $"{site.Name}-2.{dns?.Tld ?? "test"}";
        var details = dns is { Wildcard: true }
            ? Localization.Plural(Strings.ResourceManager, "Sites_AddDomainWildcard", depth, site.Domain, alias)
            : dns?.WildcardProblem is { Length: > 0 } p
                ? Localization.Format(Strings.Sites_AddDomainNoWildcardBecause, p, alias)
                : Localization.Format(Strings.Sites_AddDomainNoWildcard, alias);
        if (await Window.Ask(Localization.Format(Strings.Sites_AddDomainTitle, site.Name), details,
                Localization.Format(Strings.Sites_AddDomainPlaceholder, alias), Strings.Common_Add) is { } domain)
        {
            await Model.Sites.AddDomainAsync(site.Name, domain);
        }
    }

    private async Task RemoveDomain(Status.Site site, Status.SiteDomain domain)
    {
        if (await Window.Confirm(Localization.Format(Strings.Common_RemoveQuestion, domain.Host), Localization.Format(Strings.Sites_RemoveDomainDetails, domain.Host, site.Name), Strings.Common_Remove, true))
        {
            await Model.Sites.RemoveDomainAsync(site.Name, domain.Entry);
        }
    }

    private async Task Pin(Status.Site site, string tool, string version, string details)
    {
        var shown = tool == "root" ? SiteDetailViewModel.RootLabel(version) : version;
        var question = tool switch
        {
            "php" => Strings.Sites_PinPhpQuestion,
            "root" => Strings.Sites_PinRootQuestion,
            _ => Strings.Sites_PinNodeQuestion
        };
        if (await Window.Confirm(Localization.Format(question, shown, site.Name), details + " " + Strings.Sites_ChangesAfile, Strings.Sites_Pin))
        {
            await Model.Sites.PinAsync(site.Name, tool, version);
        }
    }

    private async Task Unpin(Status.Site site, string tool, string details)
    {
        var question = tool switch
        {
            "php" => Strings.Sites_UnpinPhpQuestion,
            "root" => Strings.Sites_UnpinRootQuestion,
            _ => Strings.Sites_UnpinNodeQuestion
        };
        if (await Window.Confirm(Localization.Format(question, site.Name), details + " " + Strings.Sites_ChangesFiles, Strings.Sites_Unpin))
        {
            await Model.Sites.UnpinAsync(site.Name, tool);
        }
    }

    // What the details ask of the pane (ISiteDetailHost).
    SitesService ISiteDetailHost.Sites => Model.Sites;
    void ISiteDetailHost.Start(string resource, Func<Task> action, bool refreshAfter) => Start(resource, action, refreshAfter);
    Task ISiteDetailHost.UpdateSiteAsync(string site, SiteChange change, Func<Action?>? draft) => UpdateSiteAsync(site, change, draft);
    void ISiteDetailHost.Post(Action action) => DispatcherQueue.TryEnqueue(() => action());
    void ISiteDetailHost.Open(Status.Site site) => Model.Open(site);
    void ISiteDetailHost.OpenUrl(string url) => Model.Open(url);
    void ISiteDetailHost.Reveal(string path) => Model.Reveal(path);
    void ISiteDetailHost.OpenLog(string log) => App.Current.OpenLog(new LogsViewModel.ServiceSource(log));
    void ISiteDetailHost.Copy(string text) => Ui.Copy(text);
    void ISiteDetailHost.Select(string name) => Select(name);
    Task ISiteDetailHost.RemoveSiteAsync(Status.Site site) => RemoveSiteAsync(site);
    Task ISiteDetailHost.ShowExtensionsAsync(Status.Site site) => ShowExtensions(site);
    Task ISiteDetailHost.PinAsync(Status.Site site, string tool, string version, string details) => Pin(site, tool, version, details);
    Task ISiteDetailHost.UnpinAsync(Status.Site site, string tool, string details) => Unpin(site, tool, details);
    Task ISiteDetailHost.AddDomainAsync(Status.Site site) => AddDomain(site);
    Task ISiteDetailHost.RemoveDomainAsync(Status.Site site, Status.SiteDomain domain) => RemoveDomain(site, domain);
    Task ISiteDetailHost.ChooseRootAsync(Status.Site site) => ChooseRoot(site);
    Task ISiteDetailHost.AddBoostGuidelineAsync(Status.Site site, bool missing) => AddBoostGuideline(site, missing);
    Task ISiteDetailHost.StartShareAsync(Status.Site site) => StartShare(site);
    Task ISiteDetailHost.CopyQrAsync(string data) => CopyQr(data);
    Task ISiteDetailHost.SaveQrAsync(string data, string site) => SaveQr(data, site);
    Task ISiteDetailHost.NewWorktreeAsync(Status.Site site) => NewWorktree(site);
    Task ISiteDetailHost.RestartAsync(string site) => Restart(site);
    Task ISiteDetailHost.RestartReverbAsync(string site) => RestartReverb(site);
    Task ISiteDetailHost.RemoveWorktreeAsync(Status.Site worktree) => RemoveWorktree(worktree);
    Task ISiteDetailHost.SwitchDatabaseAsync(Status.Site worktree, string mode) => SwitchDatabase(worktree, mode);
    Task ISiteDetailHost.SetUpWorktreeAsync(Status.Site worktree, bool again) => SetUpWorktree(worktree, again);
    Task ISiteDetailHost.ShowSetupLogAsync(Status.Site worktree) => ShowSetupLog(worktree);
    Task ISiteDetailHost.RemoveProxyAsync(Status.Proxy proxy) => RemoveProxyAsync(proxy);
    Task ISiteDetailHost.ChangeUpstreamAsync(Status.Proxy proxy) => ChangeUpstreamAsync(proxy);
    Task ISiteDetailHost.ChangeCommandAsync(Status.Proxy proxy) => ChangeCommandAsync(proxy);
}

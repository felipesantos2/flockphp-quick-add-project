using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

/// <summary>The view models of a site's, worktree's and proxy's details in Settings → Sites.</summary>
public sealed class SiteDetailTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-detail-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly Host host;
    private readonly OutputViewModel output;

    public SiteDetailTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
        host = new Host(new SitesService(client, new OperationRunner()));
        output = new OutputViewModel(host.Sites, _ => { });
    }

    public void Dispose()
    {
        output.Stop();
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public void TheMenuOffersOnlyWhatCanBePinnedOrUnpinned()
    {
        var detail = new SiteDetailViewModel(host, output);

        detail.Update(Site(php: "8.4", node: "22.11.0"), null, null, null, false, false);
        Assert.Equal((true, "Pin PHP 8.4 to Project…"), (detail.ShowsPinPhp, detail.PinPhpText));
        Assert.Equal((true, "Pin Node 22 to Project…"), (detail.ShowsPinNode, detail.PinNodeText));
        Assert.Equal((true, "Pin Document Root (Project folder) to Project…"), (detail.ShowsPinRoot, detail.PinRootText));
        Assert.False(detail.ShowsMenuSeparator);
        Assert.True(detail.HasMenu);

        // Pinned in the site's own folder: Unpin instead of Pin, with a separator before it.
        detail.Update(Site(php: "8.4", node: "22", phpSource: Status.VersionSource.Project, phpFile: @"C:\code\shop\.flockrc"), null, null, null, false, false);
        Assert.False(detail.ShowsPinPhp);
        Assert.Equal((true, "Unpin PHP (.flockrc)…"), (detail.ShowsUnpinPhp, detail.UnpinPhpText));
        Assert.True(detail.ShowsMenuSeparator);
    }

    [Fact]
    public void PickingAVersionIsolatesItWithADraftUntilTheDaemonHasIt()
    {
        var detail = new SiteDetailViewModel(host, output);
        var row = new SiteRowViewModel(Site(php: "8.4"));
        var config = new FlockConfig { DefaultPhp = "8.4", Php = [new FlockConfig.PhpVersion { Version = "8.3" }, new FlockConfig.PhpVersion { Version = "8.4" }] };
        detail.Update(row.Snapshot, row, config, null, false, false);

        Assert.Equal(["Default (PHP 8.4)", "PHP 8.3", "PHP 8.4"], detail.Php.Items);
        Assert.Equal(0, detail.Php.SelectedIndex);
        Assert.Equal("The site follows Flock's default PHP version.", detail.Php.Tooltip);

        detail.Php.SelectedIndex = 1;

        var (resource, refreshAfter) = Assert.Single(host.Started);
        Assert.Equal(("shop", false), (resource, refreshAfter));
        host.RunAll();
        Assert.Equal(new SiteChange(Php: "8.3"), Assert.Single(host.Changes).Change);
        Assert.Equal("8.3", row.Php);
    }

    [Fact]
    public void AVersionIsolatedHereThatIsNoLongerInstalledStaysPickable()
    {
        var detail = new SiteDetailViewModel(host, output);
        var config = new FlockConfig { Php = [new FlockConfig.PhpVersion { Version = "8.4" }] };

        detail.Update(Site(php: "8.1", phpSource: Status.VersionSource.Isolated), null, config, null, false, false);

        Assert.Equal(["Default", "PHP 8.4", "PHP 8.1"], detail.Php.Items);
        Assert.Equal(2, detail.Php.SelectedIndex);
        Assert.Equal("PHP 8.1 is set for this site on this machine (isolated).", detail.Php.Tooltip);
    }

    [Fact]
    public void ControlsFollowingANewSnapshotDontActOnIt()
    {
        var detail = new SiteDetailViewModel(host, output);
        var config = new FlockConfig { Php = [new FlockConfig.PhpVersion { Version = "8.3" }, new FlockConfig.PhpVersion { Version = "8.4" }] };
        detail.Update(Site(php: "8.3"), null, config, null, false, false);
        // A picker the binding gives a new list may write its selection back meanwhile.
        detail.Php.PropertyChanged += (_, _) => detail.Php.SelectedIndex = 2;
        detail.PropertyChanged += (_, _) => (detail.Secured, detail.TypeIndex) = (true, 1);

        detail.Update(Site(php: "8.4", phpSource: Status.VersionSource.Isolated), null, config, null, false, false);

        Assert.Empty(host.Started);
        Assert.Equal(2, detail.Php.SelectedIndex);
    }

    [Fact]
    public void ChooseFolderAsksForOneAndTheRootPickerSnapsBack()
    {
        var detail = new SiteDetailViewModel(host, output);
        detail.Update(Site(rootSuggestions: ["public", "web"]), null, null, null, false, false);
        Assert.Equal(["Automatic (Project folder)", "public", "web", "Choose Folder…"], detail.RootItems);
        var changed = new List<string?>();
        detail.PropertyChanged += (_, args) => changed.Add(args.PropertyName);

        detail.RootIndex = 3;

        Assert.Equal(0, detail.RootIndex);
        host.RunPosted();
        Assert.Contains(nameof(SiteDetailViewModel.RootIndex), changed);
        Assert.Equal("ChooseRoot shop", Assert.Single(host.Calls(() => host.RunAll())));
    }

    [Fact]
    public void HintsSayWhenVersionsAndRootsDisagree()
    {
        var detail = new SiteDetailViewModel(host, output);
        var site = Site(php: "8.2", phpSource: Status.VersionSource.Isolated, phpPinned: "8.4", phpFile: @"C:\code\shop\.flockrc",
            root: "web", rootSource: Status.VersionSource.Isolated, rootPinned: "public");

        detail.Update(site, null, null, null, false, false);

        var php = Assert.Single(detail.VersionHints);
        Assert.Equal(("PHP 8.2 is isolated here; the project pins 8.4 in .flockrc.", "Use .flockrc", true), (php.Text, php.ActionText, php.HasAction));
        var root = Assert.Single(detail.RootHints);
        Assert.Equal("web is set here; the project sets public in .flockrc.", root.Text);
        Assert.Equal("Use .flockrc (public)", detail.RootItems[0]);
        Assert.Equal("", detail.RootEmptyNote);

        detail.Update(Site(rootState: Status.DocumentRootState.Empty, root: "public"), null, null, null, false, false);
        Assert.Empty(detail.RootHints);
        Assert.Equal("public has no index.php or index.html, so Flock shows a page instead of the site until it does.", detail.RootEmptyNote);
    }

    [Fact]
    public void ANodeAppShowsItsDevServerAndItsOutput()
    {
        var detail = new SiteDetailViewModel(host, output);
        var site = Site(type: Status.SiteType.Node, server: new Status.SiteNodeServer
        {
            State = Status.NodeServerState.Starting, Port = 22417, Command = "npm run dev", Problem = "waiting for port 22417", Log = "node-shop",
        });

        detail.Update(site, null, null, null, false, false);

        Assert.Equal((true, false), (detail.IsNode, detail.IsPhp));
        Assert.Equal(("Caution", "Starting…", "waiting for port 22417"), (detail.DevServer.Tone, detail.DevServer.StateLabel, detail.DevServer.Problem));
        Assert.Equal((false, true, false), (detail.DevServer.CanStart, detail.DevServer.CanStop, detail.DevServer.CanRestart));
        Assert.Equal(new OutputSource("node-shop", true), detail.Output);
        Assert.EndsWith("pin it (…) into .nvmrc.", detail.VersionsNote);
    }

    [Fact]
    public void ReverbsAddressIsTheSitesWithItsPort()
    {
        var site = new Status.Site { Name = "shop", Domain = "shop.test", Secured = true, Url = "https://shop.test:8443" };
        Assert.Equal("wss://shop.test:8443/app", ReverbSectionViewModel.AddressOf(site, true));
        Assert.Equal("ws://shop.test/app", ReverbSectionViewModel.AddressOf(site, false)); // HTTPS being switched off
        Assert.Equal("ws://shop.test:8080/app", ReverbSectionViewModel.AddressOf(new Status.Site { Domain = "shop.test", Url = "http://shop.test:8080" }, false));
    }

    [Fact]
    public void ReverbShowsItsStateEnvAndOutput()
    {
        var detail = new SiteDetailViewModel(host, output);
        var reverb = detail.Reverb;

        detail.Update(Site(reverbAvailable: true), null, null, null, false, false);
        Assert.Equal((false, true, true), (reverb.IsOn, reverb.CanToggle, reverb.IsOff));
        Assert.Contains("ws://shop.test/app", reverb.OffNote);
        Assert.Null(detail.Output);

        var row = new SiteRowViewModel(Site(secured: true, reverb: new Status.SiteReverb
        {
            State = Status.ReverbState.Running, Env = ["REVERB_HOST=shop.test", "REVERB_PORT=443"], Log = "reverb-shop",
        }));
        detail.Update(row.Snapshot, row, null, null, false, true);
        Assert.Equal((true, true, "Running at wss://shop.test/app"), (reverb.IsOn, reverb.IsRunning, reverb.RunningText));
        Assert.Equal("REVERB_HOST=shop.test\nREVERB_PORT=443", reverb.Env);
        // Its restart is in flight.
        Assert.Equal((true, false), (reverb.IsRestarting, reverb.CanRestart));
        Assert.Equal(new OutputSource("reverb-shop", true), detail.Output);

        detail.Update(Site(reverb: new Status.SiteReverb { State = Status.ReverbState.Unavailable }), null, null, null, false, false);
        Assert.Equal((true, "Reverb can't run."), (reverb.IsUnavailable, reverb.UnavailableText));
        Assert.Null(detail.Output);
    }

    [Fact]
    public void TurningReverbOffAsksTheDaemonAndShowsItAgainAfterwards()
    {
        var detail = new SiteDetailViewModel(host, output);
        detail.Update(Site(reverb: new Status.SiteReverb { State = Status.ReverbState.Running }), null, null, null, false, false);

        detail.Reverb.IsOn = false;

        Assert.Equal(("shop", true), Assert.Single(host.Started));
        host.RunAll();
        Assert.Equal(new SiteChange(Reverb: false), Assert.Single(host.Changes).Change);
    }

    [Fact]
    public void FrankenPhpNeedsPhp82AndShowsItsState()
    {
        var detail = new SiteDetailViewModel(host, output);
        var server = detail.Server;

        detail.Update(Site(php: "8.1"), null, null, null, false, false);
        Assert.Equal((0, false, false), (server.ServerIndex, server.CanPickFrankenPhp, server.IsFrankenPhp));

        detail.Update(Site(php: "8.4", server: Status.SiteServer.FrankenPhp, worker: Status.SiteServer.Octane, octane: true,
            frankenPhp: new Status.SiteFrankenPhp { State = "running", Version = "1.13.0", PhpVersion = "8.4.14" }), null, null, null, true, false);
        Assert.Equal((1, 1, true), (server.ServerIndex, server.ModeIndex, server.IsRunning));
        Assert.Equal("Running FrankenPHP 1.13.0 with PHP 8.4.14 · worker mode", server.RunningText);
        Assert.Equal((true, false), (server.IsRestarting, server.CanRestart));
        Assert.Null(server.ModeTooltip);

        detail.Update(Site(php: "8.4", server: Status.SiteServer.FrankenPhp,
            frankenPhp: new Status.SiteFrankenPhp { State = "installing", Progress = 0.42 }), null, null, null, false, false);
        Assert.Equal((true, "Downloading FrankenPHP for PHP 8.4… 42%", false), (server.IsInstalling, server.InstallingText, server.IsProgressIndeterminate));
        Assert.NotNull(server.ModeTooltip);

        // Picking what already runs it does nothing; the other server changes it.
        server.ServerIndex = 1;
        Assert.Empty(host.Started);
        server.ServerIndex = 0;
        host.RunAll();
        Assert.Equal(new SiteChange(Server: Status.SiteServer.FastCgi), Assert.Single(host.Changes).Change);
    }

    [Fact]
    public void ShareFollowsItsStates()
    {
        var detail = new SiteDetailViewModel(host, output);
        var share = detail.Share;

        detail.Update(Site(), null, null, null, false, false);
        Assert.Equal((true, false), (share.IsIdle, share.CanShare));

        detail.Update(Site(share: new Status.SiteShare { State = Status.ShareState.Building }), null, null, null, false, false);
        Assert.Equal((true, "Building assets…", "Stop"), (share.IsStarting, share.StartingText, share.StopText));

        detail.Update(Site(share: new Status.SiteShare
        {
            State = Status.ShareState.Running, Url = "https://a.trycloudflare.com", Allow = ["a@b.c"], Qr = "cXI=", Rebuilding = true, CanRebuild = true,
        }), null, null, null, false, false);
        Assert.Equal((true, true, "Stop sharing"), (share.IsRunning, share.HasQr, share.StopText));
        Assert.Equal(("Only for: a@b.c", "Rebuilding…", false), (share.Allowed, share.RebuildText, share.IsRebuildEnabled));

        // Running without an address yet, or failed: why it stopped, and Try again.
        detail.Update(Site(share: new Status.SiteShare { State = Status.ShareState.Failed }), null, null, null, false, false);
        Assert.Equal((true, "The share stopped.", "Dismiss", false), (share.IsStopped, share.Failure, share.StopText, share.HasQr));
    }

    [Fact]
    public void ASiteListsItsDomainsAndWorktrees()
    {
        var detail = new SiteDetailViewModel(host, output);
        var status = new Status
        {
            Dns = new Status.DnsStatus { Wildcard = false, WildcardProblem = "a policy overrides it" },
            Sites =
            [
                Site(domains:
                [
                    new Status.SiteDomain { Entry = "admin", Host = "admin.shop.test", Url = "https://admin.shop.test" },
                    new Status.SiteDomain { Entry = "shop-2.test", Host = "shop-2.test", Alias = true, Url = "https://shop-2.test" },
                    new Status.SiteDomain { Entry = "api", Host = "api.shop.test", Problem = "another site uses it" },
                ]),
                new Status.Site
                {
                    Name = "feature.shop", Domain = "feature.shop.test", Parent = "shop", Branch = "feature",
                    Worktree = new Status.WorktreeSetup { State = Status.WorktreeState.Running, Step = "composer install" },
                },
            ],
        };

        detail.Update(status.Sites[0], null, null, status, false, false);

        Assert.Equal(["admin.shop.test", "shop-2.test (alias)", "api.shop.test"], detail.Domains.Select(domain => domain.LinkText));
        Assert.Equal((true, false), (detail.Domains[2].HasProblem, detail.Domains[2].IsLink));
        Assert.Equal("api.shop.test: another site uses it", detail.Domains[2].Problem);
        Assert.Equal("Subdomains only work when you add them here: a policy overrides it.", detail.WildcardNote);
        Assert.True(detail.ShowsWildcardNote);
        var worktree = Assert.Single(detail.Worktrees);
        Assert.Equal(("feature", true, "composer install"), (worktree.Label, worktree.IsBusy, worktree.Step));
        Assert.False(detail.HasNoWorktrees);

        worktree.SelectCommand.Execute(null);
        Assert.Equal("Select feature.shop", Assert.Single(host.Log));
    }

    [Fact]
    public void AWorktreeSwitchesItsDatabaseAndThePickerSnapsBack()
    {
        var detail = new WorktreeDetailViewModel(host, output);
        var worktree = new Status.Site
        {
            Name = "feature.shop", Domain = "feature.shop.test", Path = @"C:\code\shop-feature", Parent = "shop", Php = "8.4",
            Worktree = new Status.WorktreeSetup { State = Status.WorktreeState.Ready, Database = "shop_feature", Log = "setup-feature" },
        };

        detail.Update(worktree, new Status { Sites = [Site(), worktree] });

        // Its own database in a mode left to flock.yml: none of the picker's.
        Assert.Equal((-1, "shop_feature", true), (detail.DatabaseIndex, detail.CurrentDatabase, detail.CanSwitchDatabase));
        Assert.Equal(("feature", "None (detached HEAD)", true), (detail.Label, detail.Branch, detail.HasParent));
        Assert.Equal((true, true, false), (detail.IsReady, detail.HasLog, detail.IsBusy));

        detail.DatabaseIndex = 2;

        Assert.Equal(-1, detail.DatabaseIndex);
        host.RunAll();
        Assert.Equal("SwitchDatabase feature.shop fresh", Assert.Single(host.Log));
    }

    [Fact]
    public void AWorktreeNotSetUpYetCanBeSetUp()
    {
        var detail = new WorktreeDetailViewModel(host, output);

        detail.Update(new Status.Site { Name = "fix.shop", Parent = "gone", Branch = "fix" }, new Status());

        Assert.Equal((true, false, 0), (detail.IsNotSetUp, detail.CanSwitchDatabase, detail.DatabaseIndex));
        Assert.Equal("Set the worktree up first.", detail.DatabaseTooltip);
        Assert.Equal((false, true), (detail.HasParent, detail.HasNoParent));
        detail.SetUpCommand.Execute(null);
        host.RunAll();
        Assert.Equal("SetUp fix.shop False", Assert.Single(host.Log));
    }

    [Fact]
    public void AProxyShowsItsCommandsAndTheirOutput()
    {
        var detail = new ProxyDetailViewModel(host, output);
        var proxy = new Status.Proxy
        {
            Name = "s3", Domain = "s3.test", Upstream = "http://127.0.0.1:9000",
            Command = new Status.ProxyCommand { Command = "docker compose up -d", StopCommand = "docker compose stop", Dir = @"C:\code\s3", State = Status.ProxyState.Stopping, Log = "proxy-s3" },
        };

        detail.Update(proxy);

        Assert.Equal((true, "Caution", "Stopping…", ""), (detail.HasCommands, detail.Tone, detail.StateLabel, detail.StateProblem));
        Assert.Equal((true, false, false), (detail.HasStopLine, detail.CanStart, detail.CanStop));
        Assert.Equal(new OutputSource("proxy-s3", true), detail.Output);
        Assert.Equal("Nothing answers at 127.0.0.1:9000 yet.", detail.NotRunningText);
        Assert.DoesNotContain("Flock doesn't start the server", detail.Note);

        detail.Update(new Status.Proxy { Name = "s3", Domain = "s3.test", Upstream = "http://127.0.0.1:9000" });
        Assert.Equal((false, true), (detail.HasCommands, detail.HasNoCommands));
        Assert.Null(detail.Output);
        Assert.Contains("Flock doesn't start the server", detail.Note);

        detail.Secured = true;
        Assert.Equal(("s3", true), Assert.Single(host.Started));
    }

    [Fact]
    public async Task TheOutputReadsTheLogItShows()
    {
        handler.Log = "starting\nready  \n\n";

        output.Show("node-shop", isLive: false);
        await handler.LogRead.Task.WaitAsync(TimeSpan.FromSeconds(5));
        await WaitFor(() => output.Text == "starting\nready");

        // Another log: its own text, from scratch.
        handler.Log = "";
        output.Show("reverb-shop", isLive: false);
        await WaitFor(() => output.Text == OutputViewModel.Empty);
        Assert.Equal("reverb-shop", output.Log);

        output.Stop();
        Assert.False(output.IsPolling);
    }

    [Fact]
    public async Task AReadFromBeforeAClearDoesntBringTheOutputBack()
    {
        handler.Log = "old output";
        handler.HoldReads = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        output.Show("reverb-shop", isLive: false);
        await handler.LogRead.Task.WaitAsync(TimeSpan.FromSeconds(5));

        await output.ClearCommand.ExecuteAsync(null);
        handler.HoldReads.SetResult();

        await Task.Delay(200);
        Assert.Equal(OutputViewModel.Empty, output.Text);
    }

    private static async Task WaitFor(Func<bool> condition)
    {
        for (var attempt = 0; attempt < 100 && !condition(); attempt++)
        {
            await Task.Delay(20);
        }
        Assert.True(condition());
    }

    private static Status.Site Site(string php = "8.4", string? node = null, string phpSource = Status.VersionSource.Default, string phpPinned = "",
        string phpFile = "", string root = ".", string rootSource = Status.VersionSource.Default, string rootPinned = "", string rootState = "",
        List<string>? rootSuggestions = null, string type = Status.SiteType.Php, Status.SiteNodeServer? server = null, bool secured = false,
        bool reverbAvailable = false, Status.SiteReverb? reverb = null, string serverName = Status.SiteServer.FastCgi, Status.SiteShare? share = null,
        List<Status.SiteDomain>? domains = null) => new()
    {
        Name = "shop", Domain = "shop.test", Path = @"C:\code\shop", Php = php, PhpSource = phpSource, PhpPinned = phpPinned, PhpFile = phpFile,
        Node = node, Root = root, RootSource = rootSource, RootPinned = rootPinned, RootState = rootState, RootSuggestions = rootSuggestions ?? [],
        Type = type, NodeServer = server, Secured = secured, ReverbAvailable = reverbAvailable, Reverb = reverb, Server = serverName, Share = share,
        Domains = domains ?? [],
    };

    private static Status.Site Site(string php, string server, Status.SiteFrankenPhp frankenPhp, string worker = "", bool octane = false) => new()
    {
        Name = "shop", Domain = "shop.test", Path = @"C:\code\shop", Php = php, Server = server, Worker = worker, OctaneAvailable = octane, FrankenPhp = frankenPhp,
    };

    /// <summary>The pane, as the details see it: what they start, change and open.</summary>
    private sealed class Host(SitesService sites) : ISiteDetailHost
    {
        private readonly List<Func<Task>> pending = [];
        private readonly List<Action> posted = [];

        public SitesService Sites { get; } = sites;
        public List<(string Resource, bool RefreshAfter)> Started { get; } = [];
        public List<(string Site, SiteChange Change)> Changes { get; } = [];
        public List<string> Log { get; } = [];

        public void Start(string resource, Func<Task> action, bool refreshAfter = true)
        {
            Started.Add((resource, refreshAfter));
            pending.Add(action);
        }

        public void RunAll()
        {
            foreach (var action in pending.ToList())
            {
                action().GetAwaiter().GetResult();
            }
            pending.Clear();
        }

        public void RunPosted()
        {
            posted.ForEach(action => action());
            posted.Clear();
        }

        /// <summary>What running something logged.</summary>
        public List<string> Calls(Action run)
        {
            Log.Clear();
            run();
            return Log;
        }

        public Task UpdateSiteAsync(string site, SiteChange change, Func<Action?>? draft)
        {
            draft?.Invoke();
            Changes.Add((site, change));
            return Task.CompletedTask;
        }

        public void Post(Action action) => posted.Add(action);
        public void Open(Status.Site site) => Log.Add($"Open {site.Name}");
        public void OpenUrl(string url) => Log.Add($"OpenUrl {url}");
        public void Reveal(string path) => Log.Add($"Reveal {path}");
        public void OpenLog(string log) => Log.Add($"OpenLog {log}");
        public void Copy(string text) => Log.Add($"Copy {text}");
        public void Select(string name) => Log.Add($"Select {name}");
        public Task RemoveSiteAsync(Status.Site site) => Logged($"RemoveSite {site.Name}");
        public Task ShowExtensionsAsync(Status.Site site) => Logged($"ShowExtensions {site.Name}");
        public Task PinAsync(Status.Site site, string tool, string version, string details) => Logged($"Pin {site.Name} {tool} {version}");
        public Task UnpinAsync(Status.Site site, string tool, string details) => Logged($"Unpin {site.Name} {tool}");
        public Task AddDomainAsync(Status.Site site) => Logged($"AddDomain {site.Name}");
        public Task RemoveDomainAsync(Status.Site site, Status.SiteDomain domain) => Logged($"RemoveDomain {site.Name} {domain.Entry}");
        public Task ChooseRootAsync(Status.Site site) => Logged($"ChooseRoot {site.Name}");
        public Task AddBoostGuidelineAsync(Status.Site site, bool missing) => Logged($"AddBoostGuideline {site.Name} {missing}");
        public Task StartShareAsync(Status.Site site) => Logged($"StartShare {site.Name}");
        public Task CopyQrAsync(string data) => Logged("CopyQr");
        public Task SaveQrAsync(string data, string site) => Logged($"SaveQr {site}");
        public Task NewWorktreeAsync(Status.Site site) => Logged($"NewWorktree {site.Name}");
        public Task RestartAsync(string site) => Logged($"Restart {site}");
        public Task RestartReverbAsync(string site) => Logged($"RestartReverb {site}");
        public Task RemoveWorktreeAsync(Status.Site worktree) => Logged($"RemoveWorktree {worktree.Name}");
        public Task SwitchDatabaseAsync(Status.Site worktree, string mode) => Logged($"SwitchDatabase {worktree.Name} {mode}");
        public Task SetUpWorktreeAsync(Status.Site worktree, bool again) => Logged($"SetUp {worktree.Name} {again}");
        public Task ShowSetupLogAsync(Status.Site worktree) => Logged($"ShowSetupLog {worktree.Name}");
        public Task RemoveProxyAsync(Status.Proxy proxy) => Logged($"RemoveProxy {proxy.Name}");
        public Task ChangeUpstreamAsync(Status.Proxy proxy) => Logged($"ChangeUpstream {proxy.Name}");
        public Task ChangeCommandAsync(Status.Proxy proxy) => Logged($"ChangeCommand {proxy.Name}");

        private Task Logged(string call)
        {
            Log.Add(call);
            return Task.CompletedTask;
        }
    }

    private sealed class Handler : HttpMessageHandler
    {
        public string Log { get; set; } = "";
        public TaskCompletionSource LogRead { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        /// <summary>When set, a log read answers (with the log as it was when asked) only once it completes.</summary>
        public TaskCompletionSource? HoldReads { get; set; }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath.StartsWith("/v1/logs/services/", StringComparison.Ordinal))
            {
                if (request.Method == HttpMethod.Delete)
                {
                    Log = "";
                    return new HttpResponseMessage(HttpStatusCode.NoContent);
                }
                var log = Log;
                LogRead.TrySetResult();
                if (HoldReads is { } hold)
                {
                    await hold.Task.WaitAsync(cancellationToken);
                }
                return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(log) };
            }
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("{}") };
        }
    }
}

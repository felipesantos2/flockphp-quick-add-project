using System.Net;
using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.Infrastructure;
using Flock.Tray.ViewModels;

namespace Flock.Tray.Tests;

public sealed class SitesWorkflowTests : IDisposable
{
    private readonly string directory = Directory.CreateTempSubdirectory("flock-sites-").FullName;
    private readonly Handler handler = new();
    private readonly HttpClient http;
    private readonly FlockClient client;
    private readonly SitesService service;

    public SitesWorkflowTests()
    {
        var config = Path.Combine(directory, "api.json");
        File.WriteAllText(config, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"test\"}");
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        client = new FlockClient(http, config);
        service = new SitesService(client, new OperationRunner());
        service.OnChange = () => client.StatusAsync().ContinueWith(_ => { }, TaskScheduler.Default);
    }
    public void Dispose()
    {
        client.Dispose();
        http.Dispose();
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task FailedWriteStillRunsAuthoritativeStatusRefreshAndKeepsActionError()
    {
        handler.FailUpdate = true;

        var outcome = await service.UpdateAsync("site", new SiteChange(Secured: true));

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Equal("write failed", service.ErrorFor("site"));
        Assert.Equal(1, handler.StatusReads);
    }

    [Fact]
    public async Task FailedLinkKeepsItsActionErrorAfterTheStatusRefresh()
    {
        handler.FailLink = true;

        var outcome = await service.LinkAsync(@"C:\code\shop");

        Assert.Equal(OperationOutcome.Failed, outcome);
        Assert.Equal("link failed", service.ErrorFor(@"C:\code\shop"));
        Assert.Equal(1, handler.LinkWrites);
        Assert.Equal(1, handler.StatusReads);
    }

    [Fact]
    public async Task DuplicateLinksAreRejectedUntilTheFirstLinkCompletes()
    {
        handler.HoldLink = true;
        var first = service.LinkAsync(@"C:\code\shop");
        await handler.LinkStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));

        Assert.Equal(OperationOutcome.Rejected, await service.LinkAsync(@"C:\code\shop"));

        handler.ReleaseLink.TrySetResult();
        Assert.Equal(OperationOutcome.Succeeded, await first);
        Assert.Equal(1, handler.LinkWrites);
    }

    [Fact]
    public async Task ANewExtensionSelectionRetiresTheOlderReport()
    {
        var old = service.SiteExtensionsAsync("old");
        await handler.OldExtensionStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
        var current = service.SiteExtensionsAsync("current");

        var fresh = await current;
        handler.ReleaseOldExtension.TrySetResult();
        var retired = await old;

        Assert.Equal("current", fresh.Report?.Php);
        Assert.Null(retired.Report);
        Assert.Null(retired.Error);
    }

    [Theory]
    [InlineData("start", false)]
    [InlineData("rebuild", false)]
    [InlineData("stop", false)]
    [InlineData("start", true)]
    [InlineData("rebuild", true)]
    [InlineData("stop", true)]
    public async Task ShareActionsRefreshStatusAndKeepSiteScopedFailures(string action, bool fail)
    {
        handler.FailShare = fail;

        var outcome = await ShareAsync(action);

        Assert.Equal(fail ? OperationOutcome.Failed : OperationOutcome.Succeeded, outcome);
        Assert.Equal(fail ? "share failed" : null, service.ErrorFor("site"));
        Assert.Null(service.ErrorFor("other"));
        Assert.Equal(1, handler.ShareWrites);
        Assert.Equal(1, handler.StatusReads);
    }

    [Fact]
    public async Task ShareAndSiteChangesUseTheSameMutationGate()
    {
        handler.HoldShare = true;
        var started = service.StartShareAsync("site", [], false);
        await handler.ShareStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
        try
        {
            Assert.True(service.IsRunning("site"));
            Assert.Equal(OperationOutcome.Rejected, await service.StopShareAsync("site"));
            Assert.Equal(OperationOutcome.Rejected, await service.UpdateAsync("site", new SiteChange(Root: "public")));
            Assert.Equal(1, handler.ShareWrites);
            Assert.Equal(0, handler.StatusReads);
        }
        finally
        {
            handler.ReleaseShare.TrySetResult();
            await started;
        }
        Assert.False(service.IsRunning("site"));
        Assert.Equal(1, handler.StatusReads);
    }

    [Theory]
    [InlineData("start")]
    [InlineData("rebuild")]
    [InlineData("stop")]
    public async Task ShareCancellationReachesTransportAndReleasesMutationGate(string action)
    {
        handler.HoldShare = true;
        using var cancellation = new CancellationTokenSource();
        var operation = ShareAsync(action, cancellation.Token);
        await handler.ShareStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));

        cancellation.Cancel();

        Assert.Equal(OperationOutcome.Canceled, await operation.WaitAsync(TimeSpan.FromSeconds(5)));
        Assert.False(service.IsRunning("site"));
        Assert.Null(service.ErrorFor("site"));
        Assert.Equal(1, handler.ShareWrites);
    }

    private Task<OperationOutcome> ShareAsync(string action, CancellationToken token = default) => action switch
    {
        "start" => service.StartShareAsync("site", ["colleague@example.com"], true, token),
        "rebuild" => service.RebuildShareAsync("site", token),
        "stop" => service.StopShareAsync("site", token),
        _ => throw new ArgumentOutOfRangeException(nameof(action)),
    };

    [Fact]
    public async Task EquivalentExtensionReadsJoinAndBothReceiveTheReport()
    {
        var first = service.SiteExtensionsAsync("old");
        await handler.OldExtensionStarted.Task.WaitAsync(TimeSpan.FromSeconds(5));
        var second = service.SiteExtensionsAsync("old");
        handler.ReleaseOldExtension.TrySetResult();

        var results = await Task.WhenAll(first, second);

        Assert.All(results, result => Assert.Equal("old", result.Report?.Php));
    }

    [Fact]
    public void StatusRefreshKeepsTheSelectedRowAndUncommittedDraft()
    {
        var model = new SitesViewModel(service);
        model.Apply(new Status { Sites = [Site("site", "8.3")] });
        var retained = Assert.Single(model.Sites);
        retained.SetDraftPhp("8.4");

        model.Apply(new Status { Sites = [Site("site", "8.3"), Site("other", "8.2")] });

        Assert.Same(retained, model.Sites[0]);
        Assert.Equal("8.4", retained.Php);
    }

    [Fact]
    public void WhatFlockRunsRerendersTheDetailAndShowsAsADot()
    {
        var model = new SitesViewModel(service);
        Status.Site Node(string state) => new()
        {
            Name = "web", Domain = "web.test", Path = "C:\\code\\web", Type = Status.SiteType.Node,
            NodeServer = new() { State = state, Port = 22417, Command = "npm run dev -- --port 22417", Log = "node-web" },
        };
        Status.Proxy Proxy(string state) => new()
        {
            Name = "s3", Domain = "s3.test", Upstream = "http://127.0.0.1:9000",
            Command = new() { Command = "docker compose up -d", Dir = "C:\\code\\s3", State = state, Log = "proxy-s3" },
        };
        model.Apply(new Status { Sites = [Node(Status.NodeServerState.Sleeping), Site("shop", "8.4")], Proxies = [Proxy(Status.ProxyState.Stopped)] });

        var web = model.Sites[0];
        Assert.Equal((true, "Neutral", "Sleeping · starts on the next visit"), (web.HasDot, web.Dot, web.DotTooltip));
        // A PHP site has no dot.
        Assert.False(model.Sites[1].HasDot);

        // The dev server falling asleep or starting rerenders the details (the status poll brings it).
        Assert.True(model.Apply(new Status { Sites = [Node(Status.NodeServerState.Running), Site("shop", "8.4")], Proxies = [Proxy(Status.ProxyState.Stopped)] }));
        Assert.Equal("Success", web.Dot);

        model.Select("s3");
        Assert.True(model.Apply(new Status { Sites = [Node(Status.NodeServerState.Running), Site("shop", "8.4")], Proxies = [Proxy(Status.ProxyState.Failed)] }));
        Assert.Equal("Critical", model.Proxies[0].Dot);
        Assert.False(model.Apply(new Status { Sites = [Node(Status.NodeServerState.Running), Site("shop", "8.4")], Proxies = [Proxy(Status.ProxyState.Failed)] }));
    }

    [Fact]
    public void OneSelectionCoversSitesAndProxies()
    {
        var model = new SitesViewModel(service);
        Status.Proxy[] proxies = [new() { Name = "softpack", Domain = "softpack.test", Upstream = "http://127.0.0.1:3000" }];
        model.Apply(new Status { Sites = [Site("acme", "8.4")], Proxies = [.. proxies] });

        Assert.Equal("acme", model.Selected?.Name);
        Assert.True(model.HasProxies);
        Assert.Equal("softpack.test → 127.0.0.1:3000", model.Proxies[0].Route);

        model.Select("softpack");
        Assert.Null(model.Selected);
        Assert.Same(model.Proxies[0], model.SelectedProxy);
        // A proxy's change rerenders its details; the same status does not.
        Assert.False(model.Apply(new Status { Sites = [Site("acme", "8.4")], Proxies = [.. proxies] }));
        Assert.True(model.Apply(new Status { Sites = [Site("acme", "8.4")], Proxies = [new() { Name = "softpack", Domain = "softpack.test", Upstream = "http://127.0.0.1:3000", Running = true }] }));

        // The proxy gone: the first site again.
        model.Apply(new Status { Sites = [Site("acme", "8.4")] });
        Assert.Null(model.SelectedProxy);
        Assert.Equal("acme", model.Selected?.Name);

        // Only proxies: the pane isn't empty and the first proxy is selected.
        model.Apply(new Status { Proxies = [.. proxies] });
        Assert.True(model.HasSites);
        Assert.Equal("softpack", model.SelectedProxy?.Name);
    }

    [Fact]
    public void ListsWorktreesRightAfterTheirSite()
    {
        var model = new SitesViewModel(service);
        model.Apply(new Status { Sites = [Site("acme", "8.4"), Site("blog", "8.3"), Worktree("feature.acme", "acme")] });

        Assert.Equal(["acme", "feature.acme", "blog"], model.Sites.Select(row => row.Name));
    }

    [Fact]
    public void AWorktreeChangingRefreshesItsSelectedSite()
    {
        var model = new SitesViewModel(service);
        model.Apply(new Status { Sites = [Site("acme", "8.4"), Site("blog", "8.3")] });
        model.Select("acme");

        Assert.True(model.Apply(new Status { Sites = [Site("acme", "8.4"), Site("blog", "8.3"), Worktree("feature.acme", "acme")] }));
        Assert.False(model.Apply(new Status { Sites = [Site("acme", "8.4"), Site("blog", "8.3"), Worktree("feature.acme", "acme")] }));
        Assert.True(model.Apply(new Status { Sites = [Site("acme", "8.4"), Site("blog", "8.3")] }));
    }

    [Fact]
    public void StatusRefreshKeepsTheSelectedRowProjection()
    {
        var model = new SitesViewModel(service);
        model.Apply(new Status { Sites = [Site("site", "8.3"), Site("other", "8.2")] });
        var changed = new List<string?>();
        model.PropertyChanged += (_, args) => changed.Add(args.PropertyName);
        model.Select("other");
        var selected = Assert.Single(model.Sites, row => row.Name == "other");

        model.Apply(new Status { Sites = [Site("site", "8.4"), Site("other", "8.3")] });

        Assert.Same(selected, model.Selected);
        Assert.Equal("8.3", selected.Php);
        Assert.Contains(nameof(SitesViewModel.Selected), changed);
        Assert.Contains(nameof(SitesViewModel.Error), changed);
    }

    [Fact]
    public void SiteSummaryUsesTheOrderedDiagnostics()
    {
        var model = new SitesViewModel(service);
        model.Apply(new Status
        {
            Sites =
            [
                Site("problem", "8.3", problem: "site problem", notice: "site notice", domains: [Domain("tenant1", "tenant1.problem.test", "domain problem")], server: Status.SiteServer.FrankenPhp, frankenPhp: Franken("franken problem")),
                Site("notice", "8.3", notice: "site notice"),
                Site("domain", "8.3", domains: [Domain("tenant1", "tenant1.domain.test", "domain problem")]),
                Site("franken", "8.3", server: Status.SiteServer.FrankenPhp, frankenPhp: Franken("franken problem"))
            ]
        });

        Assert.Equal("site problem", model.Sites[0].Problem);
        Assert.Equal("site notice", model.Sites[1].Problem);
        Assert.Equal("tenant1.domain.test: domain problem", model.Sites[2].Problem);
        Assert.Equal("franken problem", model.Sites[3].Problem);
    }

    [Fact]
    public void UnrelatedStatusRefreshDoesNotChangeSelectedDetail()
    {
        var model = new SitesViewModel(service);
        Assert.True(model.Apply(new Status { Sites = [Site("selected", "8.3"), Site("other", "8.2")] }));
        var selected = Assert.Single(model.Sites, row => row.Name == "selected");

        Assert.False(model.Apply(new Status { Sites = [Site("selected", "8.3"), Site("other", "8.4")] }));

        Assert.Same(selected, model.Selected);
    }

    [Fact]
    public void DomainRefreshRetainsRowsAndUsesServerOrder()
    {
        var model = new SitesViewModel(service);
        model.Apply(new Status
        {
            Sites = [Site("site", "8.3", domains: [Domain("first", "first.site.test"), Domain("second", "second.site.test")])]
        });
        var site = Assert.Single(model.Sites);
        var first = site.Domains[0];
        var second = site.Domains[1];

        Assert.True(model.Apply(new Status
        {
            Sites = [Site("site", "8.3", domains: [Domain("second", "second.site.test", "changed"), Domain("third", "third.site.test")])]
        }));

        Assert.Equal(["second", "third"], site.Domains.Select(domain => domain.Entry));
        Assert.Same(second, site.Domains[0]);
        Assert.Equal("changed", site.Domains[0].Problem);
        Assert.NotSame(first, site.Domains[1]);

        Assert.True(model.Apply(new Status
        {
            Sites = [Site("site", "8.3", domains: [Domain("third", "third.site.test"), Domain("second", "second.site.test", "changed")])]
        }));

        Assert.Equal(["third", "second"], site.Domains.Select(domain => domain.Entry));
        Assert.Same(second, site.Domains[1]);
    }

    [Fact]
    public async Task FailedSiteDraftCanRollBackWhileTheActionErrorStaysVisible()
    {
        handler.FailUpdate = true;
        var model = new SitesViewModel(service);
        model.Apply(new Status { Sites = [Site("site", "8.3")] });
        var row = Assert.Single(model.Sites);
        var rollback = row.SetDraftSecured(true);

        Assert.Equal(OperationOutcome.Failed, await service.UpdateAsync("site", new SiteChange(Secured: true)));
        rollback();
        model.Apply(new Status { Sites = [Site("site", "8.3")] });

        Assert.False(row.Secured);
        Assert.Equal("write failed", service.ErrorFor("site"));
    }

    [Theory]
    [InlineData("My Blog", "my-blog")]
    [InlineData("my_app_2", "my-app-2")]
    [InlineData("--acme--", "acme")]
    [InlineData("Café App", "caf--app")]
    [InlineData("---", "")]
    public void AppSlugMatchesDaemonSiteName(string input, string expected)
    {
        Assert.Equal(expected, Flock.Tray.Settings.SitesPane.AppSlug(input));
    }

    private static Status.Site Site(string name, string php, string? problem = null, string? notice = null,
        List<Status.SiteDomain>? domains = null, string server = Status.SiteServer.FastCgi, Status.SiteFrankenPhp? frankenPhp = null) => new()
    {
        Name = name,
        Domain = name + ".test",
        Php = php,
        Problem = problem,
        Notice = notice,
        Domains = domains ?? [],
        Server = server,
        FrankenPhp = frankenPhp
    };
    private static Status.SiteDomain Domain(string entry, string host, string? problem = null) => new() { Entry = entry, Host = host, Problem = problem };
    private static Status.SiteFrankenPhp Franken(string problem) => new() { State = "failed", Problem = problem };

    private static Status.Site Worktree(string name, string parent) => new() { Name = name, Domain = name + ".test", Php = "8.4", Parent = parent };

    private sealed class Handler : HttpMessageHandler
    {
        public bool FailLink { get; set; }
        public bool FailUpdate { get; set; }
        public bool FailShare { get; set; }
        public bool HoldLink { get; set; }
        public bool HoldShare { get; set; }
        public int LinkWrites { get; private set; }
        public int ShareWrites { get; private set; }
        public TaskCompletionSource LinkStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseLink { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ShareStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseShare { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public int StatusReads { get; private set; }
        public TaskCompletionSource OldExtensionStarted { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource ReleaseOldExtension { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var path = request.RequestUri!.AbsolutePath;
            if (path == "/v1/sites" && request.Method == HttpMethod.Post)
            {
                LinkWrites++;
                LinkStarted.TrySetResult();
                if (HoldLink)
                {
                    await ReleaseLink.Task.WaitAsync(cancellationToken);
                }
                return FailLink
                    ? Json("{\"error\":\"link failed\"}", HttpStatusCode.BadRequest)
                    : Json("{\"sites\":[{\"name\":\"shop\",\"domain\":\"shop.test\",\"php\":\"8.3\"}]}");
            }
            if (path.StartsWith("/v1/shares/", StringComparison.Ordinal))
            {
                ShareWrites++;
                ShareStarted.TrySetResult();
                if (HoldShare)
                {
                    await ReleaseShare.Task.WaitAsync(cancellationToken);
                }
                return FailShare
                    ? Json("{\"error\":\"share failed\"}", HttpStatusCode.BadRequest)
                    : Json(request.Method == HttpMethod.Delete ? "[]" : "{\"site\":\"site\",\"state\":\"building\"}");
            }
            if (path == "/v1/status")
            {
                StatusReads++;
                return Json("{\"sites\":[{\"name\":\"site\",\"domain\":\"site.test\",\"php\":\"8.3\"}]}");
            }
            if (path == "/v1/sites/site" && FailUpdate)
            {
                return Json("{\"error\":\"write failed\"}", HttpStatusCode.BadRequest);
            }
            if (path.EndsWith("/extensions", StringComparison.Ordinal))
            {
                var site = path.Split('/')[3];
                if (site == "old")
                {
                    OldExtensionStarted.TrySetResult();
                    await ReleaseOldExtension.Task;
                }
                return Json($"{{\"php\":\"{site}\",\"extensions\":[]}}");
            }
            return Json("{}");
        }

        private static HttpResponseMessage Json(string body, HttpStatusCode status = HttpStatusCode.OK) => new(status)
        {
            Content = new StringContent(body),
        };
    }
}

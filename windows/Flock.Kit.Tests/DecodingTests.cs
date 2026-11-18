namespace Flock.Kit.Tests;

public class DecodingTests
{
    [Fact]
    public void DecodesNullListsAsEmpty()
    {
        Assert.Empty(FlockJson.Decode<List<string>>("null"));
    }

    [Fact]
    public void ParsesGoTimesWithAndWithoutFractions()
    {
        var whole = FlockJson.ParseDate("2026-10-13T13:40:07+02:00")!.Value;
        var nanos = FlockJson.ParseDate("2026-10-13T13:40:07.740103123+02:00")!.Value;
        var utc = FlockJson.ParseDate("2026-10-13T11:40:07.5Z")!.Value;

        Assert.True(Math.Abs((nanos - whole).TotalSeconds - 0.740103123) < 0.000001);
        Assert.Equal(0.5, (utc - whole).TotalSeconds);
        Assert.Null(FlockJson.ParseDate("yesterday"));
    }

    [Fact]
    public void DecodesAFullStatus()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"0893ae2","channel":"beta","pid":91994,"started_at":"2026-10-13T14:10:01.123456+02:00",
             "dns":{"tld":"test","addr":"127.0.0.1:53535"},"http_port":80,"https_port":443,
             "mail":"127.0.0.1:2525","postgres":"18.6 on 127.0.0.1:5432",
             "extras":[{"id":"redis","name":"Redis","version":"7.2.16","port":6379,"enabled":true,"up":true},{"id":"mysql"}],
             "xdebug":true,"xdebug_breakpoints":true,"dumps":false,"mail_count":3,"dump_count":12,
             "sites":[{"name":"watchtower","domain":"watchtower.test","path":"/code/watchtower","php":"8.5","secured":true,"parked":false,"url":"https://watchtower.test","debugging":true},
                      {"name":"legacy","domain":"legacy.test","path":"/code/legacy","php":"7.4","secured":false,"parked":true,"problem":"PHP 7.4 is not installed"}],
             "processes":[{"name":"php8.5-fpm","up":true,"restarts":0},{"name":"postgres","up":false,"restarts":3,"last_error":"exit status 1: could not bind"}],
             "warnings":["postgres: port 5432 is used by postgres (pid 1)"]}
            """);

        Assert.Equal(443, status.HttpsPort);
        Assert.Equal("beta", status.Channel);
        Assert.Equal(["redis", "mysql"], status.Extras.Select(e => e.Id));
        Assert.True(status.Extras[0].Up);
        Assert.Equal(6379, status.Extras[0].Port);
        Assert.Equal("mysql", status.Extras[1].Name);
        Assert.True(status.Extras[1].Enabled);
        Assert.False(status.Extras[1].Up);
        Assert.Equal(3, status.MailCount);
        Assert.Equal(12, status.DumpCount);
        Assert.Equal("https://watchtower.test", status.Sites[0].Url);
        Assert.True(status.XdebugBreakpoints);
        Assert.True(status.Sites[0].Debugging);
        Assert.False(status.Sites[1].Debugging);
        Assert.Null(status.Sites[1].Url);
        Assert.NotNull(status.Sites[1].Problem);
        Assert.Equal("exit status 1: could not bind", status.Processes[1].LastError);
        Assert.True(status.IsDegraded);
    }

    [Fact]
    public void DecodesWorktreesAndTheirSetup()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","processes":[],
             "sites":[{"name":"acme","domain":"acme.test","path":"C:\\code\\acme","php":"8.4"},
                      {"name":"feature-auth.acme","domain":"feature-auth.acme.test","path":"C:\\code\\acme-feature-auth","php":"8.4",
                       "parent":"acme","branch":"feature/auth",
                       "worktree":{"state":"running","step":"Installing Composer packages","log":"worktree-feature-auth.acme","db_mode":"copy","database":"acme_feature_auth"}},
                      {"name":"a1b2c3.acme","domain":"a1b2c3.acme.test","path":"C:\\code\\a1b2c3","php":"8.4","parent":"acme",
                       "worktree":{"state":"failed","error":"composer install failed","log":"worktree-a1b2c3.acme"}},
                      {"name":"old.acme","domain":"old.acme.test","path":"C:\\code\\old","php":"8.4","parent":"acme","branch":"old"}]}
            """);

        var (site, running, failed, untouched) = (status.Sites[0], status.Sites[1], status.Sites[2], status.Sites[3]);
        Assert.False(site.IsWorktree);
        Assert.Null(site.Worktree);
        Assert.Equal("", site.Branch);

        Assert.True(running.IsWorktree);
        Assert.Equal(("acme", "feature/auth", "feature/auth"), (running.Parent, running.Branch, running.WorktreeLabel));
        var setup = running.Worktree!;
        Assert.Equal((Status.WorktreeState.Running, "Installing Composer packages", ""), (setup.State, setup.Step, setup.Error));
        Assert.Equal(("worktree-feature-auth.acme", Status.WorktreeDatabase.Copy, "acme_feature_auth"), (setup.Log, setup.DbMode, setup.Database));
        Assert.True(setup.IsBusy);
        Assert.True(setup.HasOwnDatabase);

        // A detached HEAD: listed by the name it is served under.
        Assert.Equal("a1b2c3", failed.WorktreeLabel);
        Assert.True(failed.Worktree!.IsFailed);
        Assert.False(failed.Worktree.IsBusy);
        Assert.Equal("", failed.Worktree.DbMode);
        Assert.False(failed.Worktree.HasOwnDatabase);

        // Served, but not set up by Flock.
        Assert.True(untouched.IsWorktree);
        Assert.Null(untouched.Worktree);
    }

    [Fact]
    public void DecodesASitesBranchesAndOrdersThem()
    {
        var branches = FlockJson.Decode<SiteBranches>("""
            {"current":"develop","default":"main","local":["alpha","develop","feature/x","main"],"remote":["origin/develop","origin/main"]}
            """);
        Assert.Equal(["develop", "main", "alpha", "feature/x"], branches.Ordered());
        Assert.Equal(["origin/develop", "origin/main"], branches.Remote);

        // Detached, and no default: just the local branches.
        var detached = FlockJson.Decode<SiteBranches>("""{"current":"","default":"","local":["b","a"],"remote":null}""");
        Assert.Equal(["b", "a"], detached.Ordered());
        Assert.Empty(detached.Remote);
    }

    [Fact]
    public void SlugsABranchLikeTheDaemon()
    {
        Assert.Equal("feature-auth", SiteBranches.Slug("feature/auth"));
        Assert.Equal("fix-v1-2-a-b", SiteBranches.Slug("Fix_v1.2//a--b!"));
        Assert.Equal("", SiteBranches.Slug("ü/"));
        Assert.Equal(63, SiteBranches.Slug(new string('a', 70)).Length);
    }

    [Fact]
    public void NestsWorktreesUnderTheirSite()
    {
        static Status.Site Site(string name, string? parent = null) => new() { Name = name, Parent = parent };
        var status = new Status
        {
            Sites = [Site("acme"), Site("blog"), Site("b.acme", "acme"), Site("x.gone", "gone"), Site("a.blog", "blog"), Site("a.acme", "acme")],
        };

        Assert.Equal(["acme", "b.acme", "a.acme", "blog", "a.blog", "x.gone"], status.SitesWithWorktrees().Select(s => s.Name));
        Assert.Equal(["b.acme", "a.acme"], status.WorktreesOf("acme").Select(s => s.Name));
        Assert.Empty(status.WorktreesOf("b.acme"));
    }

    [Fact]
    public void DecodesACreatedWorktree()
    {
        var created = FlockJson.Decode<WorktreeCreated>("""
            {"path":"C:\\code\\acme-feature-auth","branch":"brave-otter","setup":{"state":"queued","log":"worktree-feature-auth.acme"}}
            """);
        Assert.Equal("C:\\code\\acme-feature-auth", created.Path);
        Assert.Equal("brave-otter", created.Branch);
        Assert.Equal(Status.WorktreeState.Queued, created.Setup.State);
        Assert.True(created.Setup.IsBusy);
    }

    [Fact]
    public void DecodesWhereASiteVersionComesFrom()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"shop","domain":"shop.test","path":"C:\\code\\shop","secured":false,"parked":false,
                       "php":"8.3","php_source":"isolated","php_pinned":"8.4","php_file":"C:\\code\\shop\\.flockrc",
                       "node":"24","node_source":"project","node_pinned":"24","node_file":"C:\\code\\shop\\.flockrc","node_shadowed":".nvmrc: 20"},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.5","secured":false,"parked":true}]}
            """);

        // Daemons before the channel was in the status: stable.
        Assert.Equal("stable", status.Channel);

        var shop = status.Sites[0];
        Assert.Equal((Status.VersionSource.Isolated, "8.4"), (shop.PhpSource, shop.PhpPinned));
        Assert.True(shop.PhpPinnedHere);
        Assert.Equal((Status.VersionSource.Project, ".nvmrc: 20"), (shop.NodeSource, shop.NodeShadowed));
        Assert.Equal(Status.VersionSource.Default, status.Sites[1].PhpSource);
        Assert.False(status.Sites[1].PhpPinnedHere);
    }

    [Fact]
    public void DecodesTheDocumentRoot()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","processes":[],
             "sites":[{"name":"zoo","domain":"zoo.test","path":"C:\\code\\zoo","php":"8.5","root":".","root_source":"default",
                       "root_pinned":"","root_file":"","root_state":"not_found","root_suggestions":["src/www"],"notice":"No document root found"},
                      {"name":"shop","domain":"shop.test","path":"C:\\code\\shop","php":"8.5","root":"web","root_source":"isolated",
                       "root_pinned":"public","root_file":"C:\\code\\shop\\.flockrc"},
                      {"name":"old","domain":"old.test","path":"C:\\code\\old","php":"8.5"},
                      {"name":"cms","domain":"cms.test","path":"C:\\code\\cms","php":"8.5","root":"src/www","root_source":"isolated","root_state":"empty"}]}
            """);

        var zoo = status.Sites[0];
        Assert.Equal((Status.DocumentRootState.NotFound, "No document root found"), (zoo.RootState, zoo.Notice));
        Assert.Equal(["src/www"], zoo.RootSuggestions);
        Assert.False(zoo.RootPinnedHere);
        var shop = status.Sites[1];
        Assert.Equal(("web", Status.VersionSource.Isolated, "public", ""), (shop.Root, shop.RootSource, shop.RootPinned, shop.RootState));
        Assert.True(shop.RootPinnedHere);
        // A daemon from before document roots: the project folder, detected.
        var old = status.Sites[2];
        Assert.Equal((".", Status.VersionSource.Default, "", null), (old.Root, old.RootSource, old.RootState, old.Notice));
        Assert.Empty(old.RootSuggestions);
        Assert.Equal((Status.DocumentRootState.Empty, null), (status.Sites[3].RootState, status.Sites[3].Notice));
    }

    [Theory]
    [InlineData(@"C:\code\zoo\src\www", "src/www")]
    [InlineData(@"c:\Code\Zoo\src\www\", "src/www")]
    [InlineData(@"C:\code\zoo", ".")]
    [InlineData(@"C:\code\zoo-2\www", null)]
    [InlineData(@"C:\code", null)]
    public void TurnsAChosenFolderIntoADocumentRoot(string folder, string? root)
    {
        var site = new Status.Site { Path = @"C:\code\zoo" };
        Assert.Equal(root, site.RootFor(folder));
    }

    [Fact]
    public void DecodesExtraDomainsAndTheWildcardState()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "dns":{"tld":"test","addr":"127.0.0.1:53","wildcard":false,"wildcard_problem":"your organization manages DNS rules (Group Policy), so Windows ignores Flock's rule for .test","max_depth":4},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"app","domain":"app.test","path":"C:\\code\\app","php":"8.5","secured":true,"parked":false,"url":"https://app.test",
                       "domains":[{"entry":"tenant1","host":"tenant1.app.test","alias":false,"url":"https://tenant1.app.test"},
                                  {"entry":"promo.test","host":"promo.test","alias":true,"problem":"promo.test is already served by promo"}]},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.5","secured":false,"parked":true,"domains":null}]}
            """);

        Assert.False(status.Dns.Wildcard);
        Assert.Contains("Group Policy", status.Dns.WildcardProblem);
        Assert.Equal(4, status.Dns.MaxDepth);
        var app = status.Sites[0];
        Assert.Equal(("tenant1", "tenant1.app.test", false), (app.Domains[0].Entry, app.Domains[0].Host, app.Domains[0].Alias));
        Assert.Equal("https://tenant1.app.test", app.Domains[0].Url);
        Assert.True(app.Domains[1].Alias);
        Assert.Null(app.Domains[1].Url);
        Assert.NotNull(app.Domains[1].Problem);
        Assert.Empty(status.Sites[1].Domains);
    }

    [Fact]
    public void DecodesProxies()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "proxies":[{"name":"softpack","domain":"softpack.test","upstream":"http://127.0.0.1:3000","secured":true,"url":"https://softpack.test","running":true,"server":"Node.js 22.23.4"},
                        {"name":"blog","domain":"blog.test","upstream":"https://127.0.0.1:8443","secured":false,"problem":"blog.test is served by a site of the same name","running":false}]}
            """);

        Assert.Equal(["softpack", "blog"], status.Proxies.Select(p => p.Name));
        var softpack = status.Proxies[0];
        Assert.True(softpack.Secured);
        Assert.Equal(("https://softpack.test", null), (softpack.Url, softpack.Problem));
        Assert.True(softpack.Running);
        Assert.Equal("Node.js 22.23.4", softpack.ServerLabel);
        Assert.Equal("127.0.0.1:3000", softpack.UpstreamLabel);
        var blog = status.Proxies[1];
        Assert.Null(blog.Url);
        Assert.NotNull(blog.Problem);
        Assert.False(blog.Running);
        Assert.Null(blog.Server);
        Assert.Equal("Not running", blog.ServerLabel);
        // Only Flock's own http:// is left out.
        Assert.Equal("https://127.0.0.1:8443", blog.UpstreamLabel);

        // An older daemon sends none, a newer one null when there are none.
        Assert.Empty(FlockJson.Decode<Status>("""{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}""").Proxies);
        Assert.Empty(FlockJson.Decode<Status>("""{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","proxies":null}""").Proxies);
    }

    [Fact]
    public void DecodesWhatFlockRunsForASiteOrProxy()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "sites":[{"name":"web","domain":"web.test","path":"C:\\code\\web","php":"","secured":true,"parked":true,"type":"node",
                       "node":"22","node_server":{"state":"running","port":22417,"command":"npm run dev -- --port 22417","log":"node-web"}},
                      {"name":"docs","domain":"docs.test","path":"C:\\code\\docs","php":"","secured":false,"parked":true,"type":"node",
                       "node_server":{"state":"failed","port":22418,"command":"npm run dev -- --port 22418","problem":"node_modules is missing: run npm install in the project","log":"node-docs"}},
                      {"name":"shop","domain":"shop.test","path":"C:\\code\\shop","php":"8.4","secured":false,"parked":true}],
             "proxies":[{"name":"s3","domain":"s3.test","upstream":"http://127.0.0.1:9000","secured":false,"running":false,
                         "command":{"command":"docker compose up -d","dir":"C:\\code\\s3","stop_command":"docker compose stop","state":"starting","log":"proxy-s3"}},
                        {"name":"api","domain":"api.test","upstream":"http://127.0.0.1:8000","secured":false,"running":false}]}
            """);

        var web = status.Sites[0];
        Assert.True(web.IsNode);
        Assert.Equal(("Running on port 22417", false, true, true), (web.NodeServer!.StateLabel, web.NodeServer.CanStart, web.NodeServer.CanStop, web.NodeServer.CanRestart));
        var docs = status.Sites[1].NodeServer!;
        Assert.Equal(("Stopped: node_modules is missing: run npm install in the project", true, false), (docs.StateLabel, docs.CanStart, docs.CanStop));
        // A site an older daemon sends without a type is a PHP site.
        Assert.Equal((false, Status.SiteType.Php), (status.Sites[2].IsNode, status.Sites[2].Type));
        Assert.Null(status.Sites[2].NodeServer);

        var s3 = status.Proxies[0].Command!;
        Assert.Equal(("docker compose stop", "Starting…", false, true, "proxy-s3"), (s3.StopCommand, s3.StateLabel, s3.CanStart, s3.CanStop, s3.Log));
        Assert.Null(status.Proxies[1].Command);
    }

    [Fact]
    public void DecodesWhatRunsASite()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"shop","domain":"shop.test","path":"C:\\code\\shop","php":"8.4","secured":false,"parked":false,
                       "server":"frankenphp","worker":"octane","octane_available":true,
                       "frankenphp":{"state":"running","version":"1.13.0","php_version":"8.4.14","port":21000,"progress":0,"log":"frankenphp-shop"}},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.4","secured":false,"parked":false,
                       "server":"frankenphp","worker":"","octane_available":false,
                       "frankenphp":{"state":"installing","port":21001,"progress":-1,"problem":"FrankenPHP for PHP 8.4 is downloading","log":"frankenphp-blog"}},
                      {"name":"legacy","domain":"legacy.test","path":"C:\\code\\legacy","php":"8.1","secured":false,"parked":true,"server":"fastcgi","worker":"","octane_available":false},
                      {"name":"old","domain":"old.test","path":"C:\\code\\old","php":"8.3","secured":false,"parked":true}]}
            """);

        var shop = status.Sites[0];
        Assert.True(shop.IsFrankenPhp);
        Assert.Equal((Status.SiteServer.Octane, true), (shop.Worker, shop.OctaneAvailable));
        Assert.True(shop.FrankenPhp!.IsRunning);
        Assert.Equal(("1.13.0", "8.4.14", 21000, "frankenphp-shop"), (shop.FrankenPhp.Version, shop.FrankenPhp.PhpVersion, shop.FrankenPhp.Port, shop.FrankenPhp.Log));
        var blog = status.Sites[1].FrankenPhp!;
        Assert.True(blog.IsInstalling);
        Assert.False(blog.HasProgress);
        Assert.Empty(blog.Version);
        Assert.NotEmpty(blog.Problem);
        Assert.False(status.Sites[2].IsFrankenPhp);
        Assert.Null(status.Sites[2].FrankenPhp);
        // An older daemon sends none of these fields.
        Assert.Equal((Status.SiteServer.FastCgi, "", false), (status.Sites[3].Server, status.Sites[3].Worker, status.Sites[3].OctaneAvailable));
        Assert.Null(status.Sites[3].FrankenPhp);
    }

    [Fact]
    public void DecodesASitesReverb()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-11-11T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"chat","domain":"chat.test","path":"C:\\code\\chat","php":"8.4","secured":true,"parked":false,"reverb_available":true,
                       "reverb":{"state":"running","port":22080,"env":["REVERB_HOST=chat.test","REVERB_PORT=443","REVERB_SCHEME=https"],"log":"reverb-chat"}},
                      {"name":"gone","domain":"gone.test","path":"C:\\code\\gone","php":"8.4","secured":false,"parked":false,"reverb_available":false,
                       "reverb":{"state":"unavailable","port":0,"problem":"The project doesn't have Laravel Reverb","log":"reverb-gone"}},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.4","secured":false,"parked":false,"reverb_available":true},
                      {"name":"old","domain":"old.test","path":"C:\\code\\old","php":"8.3","secured":false,"parked":true}]}
            """);

        var chat = status.Sites[0];
        Assert.True(chat.ReverbAvailable);
        Assert.True(chat.Reverb!.IsRunning);
        Assert.Equal((22080, "reverb-chat", ""), (chat.Reverb.Port, chat.Reverb.Log, chat.Reverb.Problem));
        Assert.Equal(["REVERB_HOST=chat.test", "REVERB_PORT=443", "REVERB_SCHEME=https"], chat.Reverb.Env);
        var gone = status.Sites[1].Reverb!;
        Assert.Equal((Status.ReverbState.Unavailable, false), (gone.State, gone.IsRunning));
        Assert.NotEmpty(gone.Problem);
        Assert.Empty(gone.Env);
        Assert.True(status.Sites[2].ReverbAvailable);
        Assert.Null(status.Sites[2].Reverb);
        // An older daemon sends neither.
        Assert.False(status.Sites[3].ReverbAvailable);
        Assert.Null(status.Sites[3].Reverb);
    }

    [Fact]
    public void DecodesASitesShare()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-19T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"","xdebug":false,"dumps":false,"mail_count":0,"dump_count":0,"processes":[],
             "sites":[{"name":"shop","domain":"shop.test","path":"C:\\code\\shop","php":"8.4","secured":true,"parked":false,
                       "share":{"site":"shop","state":"running","url":"https://quiet-river-lamp.trycloudflare.com","allow":["*@acme.com"],
                                "started_at":"2026-10-19T14:10:01Z","expires_at":"2026-10-19T22:10:05Z","built":true,"can_rebuild":true,
                                "rebuilding":false,"log":"share-shop","qr":"iVBORw0KGgo="}},
                      {"name":"blog","domain":"blog.test","path":"C:\\code\\blog","php":"8.4","secured":false,"parked":false,
                       "share":{"site":"blog","state":"failed","allow":null,"started_at":"2026-10-19T14:10:01Z","built":false,
                                "can_rebuild":false,"rebuilding":false,"error":"cloudflared exited","log":"share-blog"}},
                      {"name":"old","domain":"old.test","path":"C:\\code\\old","php":"8.3","secured":false,"parked":true}]}
            """);

        var shop = status.Sites[0];
        Assert.True(shop.IsShared);
        Assert.Equal(("https://quiet-river-lamp.trycloudflare.com", "share-shop"), (shop.Share!.Url, shop.Share.Log));
        Assert.Equal(["*@acme.com"], shop.Share.Allow);
        Assert.Equal(new DateTimeOffset(2026, 10, 19, 22, 10, 5, TimeSpan.Zero), shop.Share.ExpiresAt);
        Assert.True(shop.Share.CanRebuild);
        Assert.Equal("iVBORw0KGgo=", shop.Share.Qr);
        var blog = status.Sites[1];
        Assert.False(blog.IsShared);
        Assert.True(blog.Share!.IsFailed);
        Assert.Empty(blog.Share.Allow);
        Assert.Null(blog.Share.ExpiresAt);
        Assert.Equal("cloudflared exited", blog.Share.Error);
        Assert.Null(status.Sites[2].Share);
    }

    [Fact]
    public void DecodesTheFrankenPhpBuilds()
    {
        var state = FlockJson.Decode<FrankenPhpState>("""
            {"available":true,"checked_at":"2026-10-13T14:10:01.5+02:00","check_error":"",
             "builds":[{"php":"8.4","installed":"1.13.0","php_version":"8.4.14","latest":"8.4.26","update":true,"size":41943040,"prerelease":false,"yanked":"crashes on start","job":null},
                       {"php":"8.3","installed":"","php_version":"","latest":"8.3.36RC1","update":false,"size":40000000,"prerelease":true,
                        "job":{"action":"install","state":"running","phase":"downloading","progress":0.42,"error":"","started_at":"2026-10-13T14:11:00Z"}},
                       {"php":"8.2","installed":"","php_version":"","latest":"","update":false,"size":0,
                        "job":{"action":"install","state":"failed","phase":"downloading","progress":-1,"error":"checksum mismatch","started_at":"2026-10-13T14:11:00Z"}}]}
            """);

        Assert.True(state.Available);
        Assert.NotNull(state.CheckedAt);
        Assert.True(state.HasRunningJob);
        Assert.True(state.Builds[0].IsInstalled);
        Assert.Equal(("8.4.26", true, 41943040L), (state.Builds[0].Latest, state.Builds[0].Update, state.Builds[0].Size));
        Assert.Equal((false, "crashes on start"), (state.Builds[0].Prerelease, state.Builds[0].Yanked));
        Assert.Equal((true, ""), (state.Builds[1].Prerelease, state.Builds[1].Yanked));
        Assert.Null(state.Builds[0].Job);
        Assert.Equal((0.42, "Downloading…"), (state.Builds[1].Job!.Progress, state.Builds[1].Job!.PhaseText));
        Assert.True(state.Builds[2].Job!.IsFailed);
        Assert.Equal("checksum mismatch", state.Builds[2].Job!.Error);

        var unavailable = FlockJson.Decode<FrankenPhpState>("""{"available":false,"checked_at":null,"check_error":"offline","builds":null}""");
        Assert.False(unavailable.Available);
        Assert.Null(unavailable.CheckedAt);
        Assert.Empty(unavailable.Builds);
        Assert.False(unavailable.HasRunningJob);
    }

    [Fact]
    public void DecodesAMinimalStatusWithOmittedFields()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","dns":{"tld":"test","addr":""},
             "http_port":80,"https_port":443,"mail":"127.0.0.1:2525","xdebug":false,"dumps":false,
             "mail_count":0,"dump_count":0,"sites":null,"processes":[]}
            """);

        Assert.Null(status.Postgres);
        Assert.Empty(status.Extras);
        Assert.Empty(status.Sites);
        Assert.Empty(status.Warnings);
        Assert.False(status.IsDegraded);
        // Both defaults are set.
        Assert.Null(status.Defaults);
        // A daemon from before runtime capture: everything captured, nothing unsupported.
        Assert.Empty(status.CaptureOff);
        Assert.Empty(status.CaptureUnsupported);
        Assert.False(status.DumpsInPage);
        Assert.Equal(0, status.EventCount);
    }

    [Fact]
    public void DecodesTheCaptureSettings()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","dumps":true,"capture_off":["view","job"],"dumps_in_page":true,"event_count":42,
             "capture_unsupported":["8.1"],"sites":[],"processes":[]}
            """);

        Assert.Equal(["view", "job"], status.CaptureOff);
        Assert.True(status.DumpsInPage);
        Assert.Equal(42, status.EventCount);
        Assert.Equal(["8.1"], status.CaptureUnsupported);

        var config = FlockJson.Decode<FlockConfig>("""{"tld":"test","dumps":true,"capture_off":null,"dumps_in_page":true}""");
        Assert.Empty(config.CaptureOff);
        Assert.True(config.DumpsInPage);
    }

    [Fact]
    public void DecodesEveryEventKindAndIgnoresUnknownOnes()
    {
        var events = FlockJson.Decode<List<RuntimeEvent>>("""
            [{"id":"6","kind":"request","time":"2026-10-18T09:40:07.9Z","site":"shop","group":"g1",
              "request":{"method":"GET","uri":"https://shop.test/spike"},"status_code":200,"duration_ms":31.2},
             {"id":"5","kind":"job","time":"2026-10-18T09:40:07.8Z","group":"g2","command_line":"artisan queue:work",
              "job":"App\\Jobs\\SpikeJob","queue":"default","connection":"database","state":"failed","exception":"RuntimeException: boom","duration_ms":5.5},
             {"id":"4","kind":"view","time":"2026-10-18T09:40:07.7Z","group":"g1","view":"spike","path":"/code/shop/resources/views/spike.blade.php"},
             {"id":"3","kind":"log","time":"2026-10-18T09:40:07.6Z","group":"g1","level":"warning","message":"spike request","context":{"count":0}},
             {"id":"2","kind":"query","time":"2026-10-18T09:40:07.5Z","group":"g1","sql":"select * from users where id > ?",
              "bindings":[0,"a",null],"connection":"sqlite","duration_ms":0.42,
              "source":{"file":"/code/shop/routes/web.php","line":12,"name":"web.php"},"some_future_field":{"x":1}},
             {"id":"1","kind":"mail","time":"2026-10-18T09:40:07.4Z","group":"g1"}]
            """);

        Assert.Equal((200, 31.2), (events[0].StatusCode, events[0].DurationMs));
        Assert.Equal("GET https://shop.test/spike", events[0].Origin);
        Assert.Equal(("App\\Jobs\\SpikeJob", "failed", "RuntimeException: boom"), (events[1].Job, events[1].State, events[1].Exception));
        Assert.Equal("artisan queue:work", events[1].Origin);
        Assert.Equal("spike", events[2].View);
        Assert.Equal(("warning", "spike request"), (events[3].Level, events[3].Message));
        Assert.Contains("\"count\": 0", events[3].ContextText);
        Assert.Equal("[0, \"a\", null]", events[4].BindingsText);
        Assert.Equal(("sqlite", 0.42, 12), (events[4].Connection, events[4].DurationMs, events[4].Source?.Line));
        Assert.Null(events[4].StatusCode);
        Assert.False(events[5].IsKnown);
        Assert.True(events[4].IsKnown);
    }

    [Fact]
    public void EmptyLogContextShowsNothing()
    {
        var events = FlockJson.Decode<List<RuntimeEvent>>("""
            [{"id":"1","kind":"log","time":"2026-10-18T09:40:07Z","context":{}},{"id":"2","kind":"log","time":"2026-10-18T09:40:07Z"}]
            """);

        Assert.Equal("", events[0].ContextText);
        Assert.Equal("", events[1].ContextText);
        Assert.Equal("", events[1].BindingsText);
    }

    [Fact]
    public void DecodesTheFirstRunInstallOfTheDefaults()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "defaults":{"php":{"version":"8.5","state":"failed","error":"download failed"},"node":{"state":"waiting"}}}
            """);
        var defaults = status.Defaults!;
        Assert.Equal(("8.5", Status.DefaultState.Failed, "download failed"), (defaults.Php.Version, defaults.Php.State, defaults.Php.Error));
        Assert.True(defaults.Php.IsFailed);
        Assert.False(defaults.Php.IsPending);
        Assert.Equal(("", Status.DefaultState.Waiting, ""), (defaults.Node.Version, defaults.Node.State, defaults.Node.Error));
        Assert.True(defaults.Node.IsPending);
        Assert.False(defaults.Node.IsInstalling);

        var installing = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z",
             "defaults":{"php":{"version":"8.5","state":"done"},"node":{"version":"24","state":"installing"}}}
            """).Defaults!;
        Assert.False(installing.Php.IsPending);
        Assert.True(installing.Node.IsInstalling);
        Assert.Equal("24", installing.Node.Version);
    }

    [Fact]
    public void DecodesMailListAndDetail()
    {
        var list = FlockJson.Decode<List<MailSummary>>("""
            [{"id":"1791891607740103000-000001","inbox":"watchtower","from":"App <app@watchtower.test>","to":["you@example.com"],
              "subject":"Flock mail check ✓","date":"2026-10-13T13:40:07+02:00","size":321,"path":"/x/1.eml"}]
            """);
        Assert.Equal("Flock mail check ✓", list[0].Subject);

        var detail = FlockJson.Decode<MailDetail>("""
            {"id":"1","inbox":"default","from":"a@b.test","to":["c@d.test"],"cc":null,"subject":"Hi","date":"2026-10-13T13:40:07.1Z",
             "html":"<p>Hi</p>","text":"Hi","headers":[{"name":"Subject","value":"Hi"}],
             "attachments":[{"index":0,"filename":"invoice.pdf","content_type":"application/pdf","size":2048}]}
            """);
        Assert.Empty(detail.Cc);
        Assert.Equal("application/pdf", detail.Attachments[0].ContentType);
    }

    [Fact]
    public void DecodesDumpsWithOptionalContext()
    {
        var dumps = FlockJson.Decode<List<Dump>>("""
            [{"id":"2","time":"2026-10-13T14:00:00.999999999+02:00","site":"watchtower.test",
              "source":{"file":"/code/watchtower/app/Http/Controllers/HomeController.php","line":42,"name":"HomeController.php"},
              "request":{"method":"GET","uri":"https://watchtower.test/"},"text":"array:1 [...]","html":"<pre class=sf-dump>…</pre>"},
             {"id":"1","time":"2026-10-13T13:59:00Z","site":"","command_line":"artisan tinker","text":"1","html":"<pre>1</pre>"},
             {"id":"0","time":"2026-10-13T13:58:00Z","site":"","source":{"file":"C:\\code\\shop\\app\\Models\\Cart.php","line":7,"name":"Cart.php"},"text":"","html":""}]
            """);

        Assert.Equal("Controllers/HomeController.php:42", dumps[0].ShortSource);
        Assert.Equal("GET", dumps[0].Request?.Method);
        Assert.Null(dumps[1].Source);
        Assert.Equal("artisan tinker", dumps[1].CommandLine);
        Assert.Equal("Models/Cart.php:7", dumps[2].ShortSource);
    }

    [Fact]
    public void DecodesLogs()
    {
        var index = FlockJson.Decode<LogIndex>("""{"services":["daemon","web"],"sites":["watchtower"]}""");
        Assert.Equal(["watchtower"], index.Sites);

        var entries = FlockJson.Decode<List<LogEntry>>("""
            [{"time":"2026-10-13T12:00:00+00:00","env":"local","level":"ERROR","message":"Boom","details":"#0 stack"},
             {"time":"2026-10-13T11:00:00Z","env":"local","level":"info","message":"Hello"}]
            """);
        Assert.Equal("error", entries[0].Level);
        Assert.Empty(entries[1].Details);
    }

    [Fact]
    public void DecodesTheApiConfig()
    {
        var config = FlockJson.Decode<ApiConfig>("""{"url":"http://127.0.0.1:53531","token":"abc"}""");
        Assert.Equal(53531, new Uri(config.Url).Port);
        Assert.Equal("abc", config.Token);
    }

    [Fact]
    public void DecodesTheAiClients()
    {
        var state = FlockJson.Decode<McpState>("""
            {"clients":[{"id":"claude-code","name":"Claude Code","state":"registered","config":"C:\\Users\\ann\\.claude.json"},
                        {"id":"vscode","name":"VS Code","state":"available","problem":"Flock can't read mcp.json — add Flock to it by hand"},
                        {"id":"codex","name":"Codex","state":"missing"}],
             "command":"C:\\Users\\ann\\AppData\\Roaming\\Flock\\bin\\flock.exe","args":["mcp"],"snippet":"{}"}
            """);
        Assert.Equal(["claude-code", "vscode", "codex"], state.Clients.Select(c => c.Id));
        Assert.Equal([McpClientState.Registered, McpClientState.Available, McpClientState.Missing], state.Clients.Select(c => c.State));
        Assert.Contains("by hand", state.Clients[1].Problem);
        Assert.Equal(["mcp"], state.Args);
        Assert.EndsWith("flock.exe", state.Command);
    }

    [Fact]
    public void DecodesTheBoostStateOfSites()
    {
        var status = FlockJson.Decode<Status>("""
            {"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","sites":[{"name":"a","boost":"missing"},{"name":"b"}]}
            """);
        Assert.Equal(Status.BoostState.Missing, status.Sites[0].Boost);
        Assert.Equal("", status.Sites[1].Boost);
    }
}

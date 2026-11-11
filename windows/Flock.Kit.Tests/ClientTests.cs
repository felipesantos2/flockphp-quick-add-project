using System.Net;
using System.Text.Json;
using Microsoft.Extensions.Time.Testing;

namespace Flock.Kit.Tests;

public class ClientTests : IDisposable
{
    private readonly string dir = Directory.CreateTempSubdirectory("flock-client-").FullName;
    private readonly FakeTimeProvider clock = new();

    public void Dispose() => Directory.Delete(dir, recursive: true);

    private sealed class Handler(Func<HttpRequestMessage, HttpResponseMessage> respond) : HttpMessageHandler
    {
        public List<HttpRequestMessage> Requests { get; } = [];
        public List<string> Bodies { get; } = [];
        public List<TimeSpan> Timeouts { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Requests.Add(request);
            Bodies.Add(request.Content is null ? "" : await request.Content.ReadAsStringAsync(cancellationToken));
            Timeouts.Add(request.Options.TryGetValue(FlockClient.TimeoutOption, out var timeout) ? timeout : TimeSpan.Zero);
            return respond(request);
        }
    }

    private sealed class WaitingHandler : HttpMessageHandler
    {
        public int Calls { get; private set; }
        public TaskCompletionSource Started { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Calls++;
            Started.TrySetResult();
            await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("{}") };
        }
    }

    private string WriteApiJson(string url, string token)
    {
        var path = Path.Combine(dir, "api.json");
        File.WriteAllText(path, $$"""{"url":"{{url}}","token":"{{token}}"}""");
        return path;
    }

    [Fact]
    public async Task SendsTheBearerTokenAndDecodesNullListsAsEmpty()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("null") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:53531", "secret"), clock);

        Assert.Empty(await client.MailAsync());
        Assert.Equal("Bearer secret", handler.Requests[0].Headers.Authorization?.ToString());
        Assert.Equal("http://127.0.0.1:53531/v1/mail", handler.Requests[0].RequestUri?.ToString());
    }

    [Fact]
    public async Task DisposingTheClientDoesNotDisposeTheApplicationOwnedHttpClient()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK));
        using var http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        using (var client = new FlockClient(http, WriteApiJson("http://127.0.0.1:1", "token"), clock))
        {
        }

        using var response = await http.GetAsync("http://127.0.0.1:1");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Fact]
    public async Task ListsAndClearsEvents()
    {
        var handler = new Handler(request => request.Method == HttpMethod.Delete
            ? new HttpResponseMessage(HttpStatusCode.NoContent)
            : new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("null") });
        using var http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        using var client = new FlockClient(http, WriteApiJson("http://127.0.0.1:53531", "secret"), clock);

        Assert.Empty(await client.EventsAsync());
        Assert.Empty(await client.EventsAsync([EventKind.Query, EventKind.Log], limit: 50));
        await client.ClearEventsAsync();

        Assert.Equal("http://127.0.0.1:53531/v1/events", handler.Requests[0].RequestUri?.ToString());
        Assert.Equal("http://127.0.0.1:53531/v1/events?kind=query%2Clog&limit=50", handler.Requests[1].RequestUri?.ToString());
        Assert.Equal((HttpMethod.Delete, "/v1/events"), (handler.Requests[2].Method, handler.Requests[2].RequestUri?.AbsolutePath));
    }

    [Fact]
    public async Task RereadsApiJsonAfterUnauthorized()
    {
        var path = WriteApiJson("http://127.0.0.1:1", "old");
        var handler = new Handler(r => r.Headers.Authorization?.Parameter == "new"
            ? new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("""{"services":["web"],"sites":null}""") }
            : new HttpResponseMessage(HttpStatusCode.Unauthorized) { Content = new StringContent("""{"error":"missing or invalid token"}""") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, path, clock);

        // The daemon restarted with a new token after the client cached the old one.
        await Assert.ThrowsAsync<FlockServerException>(async () =>
        {
            File.WriteAllText(path, """{"url":"http://127.0.0.1:1","token":"old"}""");
            await client.LogsAsync();
        });
        File.WriteAllText(path, """{"url":"http://127.0.0.1:1","token":"new"}""");
        var index = await client.LogsAsync();
        Assert.Equal(["web"], index.Services);
    }

    [Fact]
    public async Task StartsRebuildsAndStopsAShare()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.Method == HttpMethod.Delete ? "[]" : """{"site":"my app","state":"building","allow":["bob@acme.com"]}"""),
        });
        using var http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        using var client = new FlockClient(http, WriteApiJson("http://127.0.0.1:53531", "secret"), clock);

        var started = await client.StartShareAsync("my app", ["bob@acme.com"], debug: true);
        await client.RebuildShareAsync("my app");
        Assert.Empty(await client.StopShareAsync("my app"));

        Assert.Equal(Status.ShareState.Building, started.State);
        Assert.Equal((HttpMethod.Post, "/v1/shares/my%20app"), (handler.Requests[0].Method, handler.Requests[0].RequestUri?.AbsolutePath));
        Assert.Equal("""{"allow":["bob@acme.com"],"debug":true}""", handler.Bodies[0]);
        Assert.Equal((HttpMethod.Post, "/v1/shares/my%20app/rebuild"), (handler.Requests[1].Method, handler.Requests[1].RequestUri?.AbsolutePath));
        Assert.Equal((HttpMethod.Delete, "/v1/shares/my%20app"), (handler.Requests[2].Method, handler.Requests[2].RequestUri?.AbsolutePath));
    }

    [Fact]
    public async Task CallsTheWorktreeEndpoints()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.RequestUri!.AbsolutePath switch
            {
                "/v1/worktrees" => """{"path":"C:\\code\\acme-feature","setup":{"state":"queued","log":"worktree-feature.acme"}}""",
                "/v1/worktrees/remove" => """{"output":"removed"}""",
                "/v1/sites/my%20app/branches" or "/v1/sites/my app/branches" => """{"current":"main","default":"main","local":["main"],"remote":[]}""",
                _ => """{"state":"running","step":"Copying the database","db_mode":"copy"}""",
            }),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        var branches = await client.SiteBranchesAsync("my app");
        var created = await client.CreateWorktreeAsync("acme", "feature", db: Status.WorktreeDatabase.Fresh);
        await client.CreateWorktreeAsync("acme", "feature", from: "main");
        var setup = await client.SetupWorktreeAsync(created.Path, again: true);
        var followed = await client.WorktreeSetupAsync(created.Path);
        await client.SwitchWorktreeDatabaseAsync(created.Path, Status.WorktreeDatabase.Shared);
        Assert.Equal("removed", await client.RemoveWorktreeAsync(created.Path, force: true));

        Assert.Equal("C:\\code\\acme-feature", created.Path);
        Assert.Equal((Status.WorktreeState.Running, "copy"), (setup.State, setup.DbMode));
        Assert.Equal(
            ["GET /v1/sites/my%20app/branches", "POST /v1/worktrees", "POST /v1/worktrees", "POST /v1/worktrees/setup", "GET /v1/worktrees/setup?path=C%3A%5Ccode%5Cacme-feature", "POST /v1/worktrees/db", "POST /v1/worktrees/remove"],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Equal(
        [
            "",
            """{"site":"acme","branch":"feature","db":"fresh"}""",
            """{"site":"acme","branch":"feature","from":"main"}""",
            """{"path":"C:\\code\\acme-feature","again":true}""",
            "",
            """{"path":"C:\\code\\acme-feature","db":"shared"}""",
            """{"path":"C:\\code\\acme-feature","force":true}""",
        ], handler.Bodies);
        // Creating runs git worktree add; removing runs the before_delete hook too.
        Assert.Equal("main", branches.Current);
        Assert.Equal("Copying the database", followed.Step);
        // Setting up again and switching the database start a job and answer at once; as changes, they wait 30 seconds at least.
        Assert.Equal([TimeSpan.FromSeconds(5), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2), TimeSpan.FromSeconds(30), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(30), TimeSpan.FromMinutes(10)], handler.Timeouts);
    }

    [Fact]
    public async Task ReportsServerErrorsWithTheDaemonsMessage()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.Conflict) { Content = new StringContent("""{"error":"PHP 8.5 is the default version"}""") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        var error = await Assert.ThrowsAsync<FlockServerException>(() => client.UninstallPhpAsync("8.5"));
        Assert.Equal(409, error.StatusCode);
        Assert.Equal("PHP 8.5 is the default version", error.Message);
        Assert.Equal(HttpMethod.Delete, handler.Requests[0].Method);
    }

    [Fact]
    public async Task IsNotRunningWithoutApiJsonOrListener()
    {
        using var missing = new FlockClient(new HttpClient { Timeout = Timeout.InfiniteTimeSpan }, Path.Combine(dir, "nope.json"), clock);
        await Assert.ThrowsAsync<FlockNotRunningException>(() => missing.StatusAsync());

        var refusedHandler = new Handler(_ => throw new HttpRequestException("connection refused"));
        using var refused = new FlockClient(new HttpClient(refusedHandler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);
        await Assert.ThrowsAsync<FlockNotRunningException>(() => refused.StatusAsync());
    }

    [Fact]
    public async Task KeepsTheInnerCauseForInvalidApiJson()
    {
        var path = Path.Combine(dir, "invalid-api.json");
        File.WriteAllText(path, "not json");
        using var http = new HttpClient { Timeout = Timeout.InfiniteTimeSpan };
        using var client = new FlockClient(http, path, clock);

        var error = await Assert.ThrowsAsync<FlockNotRunningException>(() => client.StatusAsync());

        Assert.IsType<JsonException>(error.InnerException);
    }

    [Fact]
    public async Task SendsOnlyGivenFieldsWhenLinkingSites()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent("""{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"""),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        await client.LinkSiteAsync(@"C:\code\shop");
        Assert.Equal("""{"path":"C:\\code\\shop","secured":false}""", handler.Bodies[0]);
    }

    [Fact]
    public async Task GivesSlowEndpointsLongerThanTheDefault()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("{}") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        await client.StatusAsync();
        // The daemon may ask nodejs.org first (~30 s on a cold cache).
        await client.NodeVersionsAsync();
        // Changing PHP versions waits for the daemon's work, like the Go CLI (2 min).
        await client.InstallPhpAsync("8.4");
        await client.CancelPhpInstallAsync("8.4");
        await client.UninstallPhpAsync("8.4");
        await client.PhpVersionsAsync();

        Assert.Equal(
            [TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2), TimeSpan.FromSeconds(5)],
            handler.Timeouts);
    }

    [Fact]
    public async Task CallsThePostgresVersionEndpoints()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("""{"active":"18","majors":[]}""") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        await client.PostgresVersionsAsync();
        await client.CheckPostgresReleasesAsync();
        await client.InstallPostgresAsync("17");
        await client.UpdatePostgresAsync("18");
        await client.ActivatePostgresAsync("17");
        await client.UninstallPostgresAsync("16");
        var answer = await client.UninstallPostgresAsync("15", deleteData: true);

        Assert.Equal("18", answer.Active);
        Assert.Equal(
            [
                "GET /v1/postgres/versions", "POST /v1/postgres/check", "POST /v1/postgres/versions/17/install",
                "POST /v1/postgres/versions/18/update", "POST /v1/postgres/versions/17/activate",
                "DELETE /v1/postgres/versions/16", "DELETE /v1/postgres/versions/15?delete_data=1",
            ],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        // Installs and updates download a server and wait for it; switching and uninstalling stop one.
        Assert.Equal(
            [TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60), TimeSpan.FromMinutes(10), TimeSpan.FromMinutes(10), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2), TimeSpan.FromMinutes(2)],
            handler.Timeouts);
    }

    [Fact]
    public async Task CallsTheCatalogEndpoints()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.RequestUri!.AbsolutePath.EndsWith("/log") ? "==> Downloading\n" : """{"services":[{"id":"mysql"}]}"""),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        await client.CatalogAsync();
        await client.CheckCatalogAsync();
        var started = await client.InstallCatalogServiceAsync("mysql", "8.4", 3307);
        Assert.NotNull(started["mysql"]);
        await client.InstallCatalogServiceAsync("redis");
        await client.CancelCatalogJobAsync("mysql");
        await client.DismissCatalogJobAsync("mysql");
        Assert.Equal("==> Downloading\n", await client.CatalogLogAsync("mysql"));
        await client.ActivateCatalogServiceAsync("mysql", "9.7");
        await client.UninstallCatalogServiceAsync("mysql");
        await client.UninstallCatalogServiceAsync("mysql", "8.4");
        await client.UninstallCatalogServiceAsync("mongodb", "7.0", deleteData: true);

        Assert.Equal(
            [
                "GET /v1/catalog", "POST /v1/catalog/check", "POST /v1/catalog/mysql/install", "POST /v1/catalog/redis/install",
                "POST /v1/catalog/mysql/cancel", "DELETE /v1/catalog/mysql/job", "GET /v1/catalog/mysql/log",
                "POST /v1/catalog/mysql/activate", "DELETE /v1/catalog/mysql", "DELETE /v1/catalog/mysql?line=8.4",
                "DELETE /v1/catalog/mongodb?line=7.0&delete_data=1",
            ],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Contains("\"line\":\"8.4\"", handler.Bodies[2]);
        Assert.Contains("\"port\":3307", handler.Bodies[2]);
        Assert.Contains("\"port\":0", handler.Bodies[3]);
        Assert.Contains("\"line\":\"9.7\"", handler.Bodies[7]);
        Assert.Equal(TimeSpan.FromMinutes(1), handler.Timeouts[^1]);
    }

    [Fact]
    public async Task CallsTheFrankenPhpEndpoints()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.RequestUri!.AbsolutePath.EndsWith("/log") ? "downloading…\n" : """{"available":true,"builds":[{"php":"8.4"}]}"""),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        var state = await client.FrankenPhpAsync();
        Assert.Equal("8.4", state.Builds[0].Php);
        await client.CheckFrankenPhpAsync();
        await client.InstallFrankenPhpAsync("8.4");
        await client.CancelFrankenPhpJobAsync("8.4");
        await client.DismissFrankenPhpJobAsync("8.4");
        Assert.Equal("downloading…\n", await client.FrankenPhpLogAsync("8.4"));
        await client.UninstallFrankenPhpAsync("8.4");

        Assert.Equal(
            [
                "GET /v1/frankenphp", "POST /v1/frankenphp/check", "POST /v1/frankenphp/8.4/install",
                "POST /v1/frankenphp/8.4/cancel", "DELETE /v1/frankenphp/8.4/job", "GET /v1/frankenphp/8.4/log",
                "DELETE /v1/frankenphp/8.4",
            ],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        // Changes wait 30 seconds at least (a reload may start PHP); uninstalling may stop a process first.
        Assert.Equal(
            [TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60), TimeSpan.FromSeconds(30), TimeSpan.FromSeconds(30), TimeSpan.FromSeconds(30), TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(60)],
            handler.Timeouts);
    }

    [Fact]
    public async Task SwitchesAndRestartsASitesServer()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent("""{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"""),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        await client.UpdateSiteAsync("shop", new SiteChange(Server: Status.SiteServer.FrankenPhp));
        await client.UpdateSiteAsync("shop", new SiteChange(Worker: ""));
        await client.UpdateSiteAsync("shop", new SiteChange(Secured: true));
        await client.RestartSiteAsync("shop");

        Assert.Equal(
            ["PATCH /v1/sites/shop", "PATCH /v1/sites/shop", "PATCH /v1/sites/shop", "POST /v1/sites/shop/restart"],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Equal(["""{"server":"frankenphp"}""", """{"worker":""}""", """{"secured":true}""", ""], handler.Bodies);
        // Switching servers and restarting wait for FrankenPHP to come up; other changes 30 seconds.
        Assert.Equal([TimeSpan.FromMinutes(1), TimeSpan.FromMinutes(1), TimeSpan.FromSeconds(30), TimeSpan.FromMinutes(1)], handler.Timeouts);
    }

    [Fact]
    public async Task TalksToTheProxyEndpoints()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent("""{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z"}"""),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        await client.ProxyAsync("softpack", "3000");
        await client.ProxyAsync("softpack", "3001", secured: true);
        await client.SecureProxyAsync("soft pack", false);
        await client.RemoveProxyAsync("softpack");

        Assert.Equal(
            ["POST /v1/proxies", "POST /v1/proxies", "PATCH /v1/proxies/soft%20pack", "DELETE /v1/proxies/softpack"],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        // A new proxy leaves secured out, so the daemon keeps an existing one's setting.
        Assert.Equal(
            ["""{"name":"softpack","upstream":"3000"}""", """{"name":"softpack","upstream":"3001","secured":true}""", """{"secured":false}""", ""],
            handler.Bodies);
    }

    [Fact]
    public async Task ReportsAMissingCatalogLogAsAServerError()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.NotFound) { Content = new StringContent("""{"error":"no log for redis"}""") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        var error = await Assert.ThrowsAsync<FlockServerException>(() => client.CatalogLogAsync("redis"));
        Assert.Equal(404, error.StatusCode);
    }

    [Fact]
    public async Task RetriesGetTransportFailureAfterInvalidatingTheCredentialCache()
    {
        var path = WriteApiJson("http://127.0.0.1:1", "old");
        var calls = 0;
        var handler = new Handler(r =>
        {
            calls++;
            if (calls == 1)
            {
                File.WriteAllText(path, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"new\"}");
                throw new HttpRequestException("connection reset");
            }
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("{\"services\":[],\"sites\":[]}") };
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, path, clock);

        await client.LogsAsync();

        Assert.Equal(2, handler.Requests.Count);
        Assert.Equal("Bearer new", handler.Requests[1].Headers.Authorization?.ToString());
    }

    [Fact]
    public async Task DoesNotReplayAWriteAfterTransportFailureAndRereadsBeforeTheNextRequest()
    {
        var path = WriteApiJson("http://127.0.0.1:1", "old");
        var cause = new HttpRequestException("response lost");
        var calls = 0;
        var handler = new Handler(r =>
        {
            calls++;
            if (calls == 1)
            {
                throw cause;
            }
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("{}") };
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, path, clock);

        var error = await Assert.ThrowsAsync<FlockRequestOutcomeUnknownException>(() => client.UpdateSettingsAsync(new SettingsChange(Dumps: true)));
        Assert.Same(cause, error.InnerException);
        Assert.Single(handler.Requests);

        File.WriteAllText(path, "{\"url\":\"http://127.0.0.1:1\",\"token\":\"new\"}");
        await client.StatusAsync();
        Assert.Equal("Bearer new", handler.Requests[1].Headers.Authorization?.ToString());
    }

    [Fact]
    public async Task DoesNotReplayACommittedClearWhenItsResponseIsLost()
    {
        var messages = "[{\"id\":\"old\"}]";
        var deletes = 0;
        var handler = new Handler(r =>
        {
            if (r.Method == HttpMethod.Delete)
            {
                deletes++;
                messages = "[{\"id\":\"new\"}]";
                throw new HttpRequestException("response lost after clear");
            }
            return new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent(messages) };
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);

        await Assert.ThrowsAsync<FlockRequestOutcomeUnknownException>(() => client.ClearMailAsync());
        var current = await client.MailAsync();

        Assert.Equal(1, deletes);
        Assert.Equal(["new"], current.Select(message => message.Id));
    }

    [Fact]
    public async Task DoesNotReplayAPatchAfterItsTransportFails()
    {
        var handler = new Handler(_ => throw new HttpRequestException("connection reset"));
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);

        await Assert.ThrowsAsync<FlockRequestOutcomeUnknownException>(() => client.SetServicePortAsync("mysql", 3307));

        Assert.Single(handler.Requests);
    }

    [Fact]
    public async Task RetriesAWriteAfterItsInitialUnauthorizedResponse()
    {
        var calls = 0;
        var handler = new Handler(r => ++calls == 1
            ? new HttpResponseMessage(HttpStatusCode.Unauthorized)
            : new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("{}") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);

        await client.UpdateSettingsAsync(new SettingsChange(Dumps: true));

        Assert.Equal(2, handler.Requests.Count);
    }

    [Fact]
    public async Task UnauthorizedThenTransportFailureHasDistinctReadAndWriteOutcomes()
    {
        var readCause = new HttpRequestException("read connection lost");
        var readCalls = 0;
        var readHandler = new Handler(r => ++readCalls == 1
            ? new HttpResponseMessage(HttpStatusCode.Unauthorized)
            : throw readCause);
        using var readClient = new FlockClient(new HttpClient(readHandler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);

        var readError = await Assert.ThrowsAsync<FlockNotRunningException>(() => readClient.StatusAsync());
        Assert.Same(readCause, readError.InnerException);
        Assert.Equal(2, readHandler.Requests.Count);

        var writeCause = new HttpRequestException("write connection lost");
        var writeCalls = 0;
        var writeHandler = new Handler(r => ++writeCalls == 1
            ? new HttpResponseMessage(HttpStatusCode.Unauthorized)
            : throw writeCause);
        using var writeClient = new FlockClient(new HttpClient(writeHandler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);

        var writeError = await Assert.ThrowsAsync<FlockRequestOutcomeUnknownException>(() => writeClient.UpdateSettingsAsync(new SettingsChange(Dumps: true)));
        Assert.Same(writeCause, writeError.InnerException);
        Assert.Equal(2, writeHandler.Requests.Count);
    }

    [Fact]
    public async Task KeepsTheInnerCauseWhenAResponseCannotBeDecoded()
    {
        var handler = new Handler(_ => new HttpResponseMessage(HttpStatusCode.OK) { Content = new StringContent("not json") });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);

        var error = await Assert.ThrowsAsync<FlockException>(() => client.StatusAsync());

        Assert.IsType<JsonException>(error.InnerException);
    }

    [Fact]
    public async Task CallerCancellationIsNotRetriedOrWrapped()
    {
        var handler = new WaitingHandler();
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);
        using var cancellation = new CancellationTokenSource();

        var request = client.UpdateSettingsAsync(new SettingsChange(Dumps: true), cancellation.Token);
        await handler.Started.Task.WaitAsync(TimeSpan.FromSeconds(60));
        cancellation.Cancel();
        var error = await Assert.ThrowsAsync<TaskCanceledException>(() => request);

        Assert.IsAssignableFrom<OperationCanceledException>(error);
        Assert.Equal(1, handler.Calls);
    }

    [Fact]
    public async Task EndpointTimeoutIsVisibleWithItsCancellationCause()
    {
        var handler = new WaitingHandler();
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "token"), clock);

        var request = client.StatusAsync();
        await handler.Started.Task.WaitAsync(TimeSpan.FromSeconds(60));
        clock.Advance(TimeSpan.FromSeconds(4));
        Assert.False(request.IsCompleted);
        clock.Advance(TimeSpan.FromSeconds(1));
        var error = await Assert.ThrowsAsync<FlockException>(() => request);

        Assert.Equal("Flock did not answer GET /v1/status in time", error.Message);
        Assert.IsAssignableFrom<OperationCanceledException>(error.InnerException);
        Assert.Equal(1, handler.Calls);
    }

    [Fact]
    public async Task CallsTheAiClientAndBoostEndpoints()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.RequestUri!.AbsolutePath.EndsWith("/boost")
                ? """{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","sites":[]}"""
                : """{"clients":[{"id":"cursor","name":"Cursor","state":"registered"}],"command":"flock.exe","args":["mcp"],"snippet":"{}"}"""),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        await client.McpAsync();
        var added = await client.RegisterMcpAsync("cursor");
        Assert.Equal(McpClientState.Registered, added.Clients[0].State);
        await client.UnregisterMcpAsync("cursor");
        await client.AddBoostGuidelineAsync("my app");

        Assert.Equal(
            ["GET /v1/mcp", "POST /v1/mcp/clients/cursor", "DELETE /v1/mcp/clients/cursor", "POST /v1/sites/my%20app/boost"],
            handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Equal(TimeSpan.FromSeconds(30), handler.Timeouts[1]);
        Assert.Equal(TimeSpan.FromMinutes(2), handler.Timeouts[^1]);
    }

    [Fact]
    public async Task ListsAppTemplatesAndCreatesApps()
    {
        var handler = new Handler(r => new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = new StringContent(r.Method == HttpMethod.Get
                ? """{"templates":[{"name":"blank","requires_db":false},{"name":"wordpress","requires_db":true}]}"""
                : """{"version":"dev","pid":1,"started_at":"2026-10-13T14:10:01Z","sites":[]}"""),
        });
        using var client = new FlockClient(new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan }, WriteApiJson("http://127.0.0.1:1", "t"), clock);

        var templates = await client.AppTemplatesAsync();
        Assert.Equal(2, templates.Count);
        Assert.True(templates[1].RequiresDb);

        await client.CreateAppAsync("my-blog", @"C:\Sites", "wordpress", db: true, dbService: "mysql", secured: true);

        Assert.Equal(["GET /v1/apps/templates", "POST /v1/apps"], handler.Requests.Select(r => $"{r.Method} {r.RequestUri!.PathAndQuery}"));
        Assert.Equal(TimeSpan.FromMinutes(3), handler.Timeouts[1]);
        using var payload = JsonDocument.Parse(handler.Bodies[1]);
        Assert.Equal("my-blog", payload.RootElement.GetProperty("name").GetString());
        Assert.Equal(@"C:\Sites", payload.RootElement.GetProperty("parent_dir").GetString());
        Assert.Equal("wordpress", payload.RootElement.GetProperty("type").GetString());
        Assert.True(payload.RootElement.GetProperty("db").GetBoolean());
        Assert.Equal("mysql", payload.RootElement.GetProperty("db_service").GetString());
        Assert.True(payload.RootElement.GetProperty("secured").GetBoolean());
    }
}

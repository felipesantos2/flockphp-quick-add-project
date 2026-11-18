using Flock.Tray.Infrastructure;
using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.Web.WebView2.Core;

namespace Flock.Tray;

/// <summary>
/// Renders HTML in WebView2. Mail HTML runs with JavaScript off and remote loads blocked; dumps
/// enable JavaScript for their local collapsible output while still blocking remote loads.
/// </summary>
internal sealed class HtmlView : Grid
{
    private const int MaxInlineLength = 1_500_000;

    private readonly WebView2 web = new();
    private readonly InfoBar shellError = Ui.ErrorBar();
    private readonly bool allowScripts;
    private readonly bool blockRemote;
    private readonly HtmlPageFiles pages;
    private readonly IShellLauncher shell;
    private readonly IUiDispatcher? dispatcher;
    private readonly Lock gate = new();
    private readonly Task initialization;
    private readonly HashSet<Task> loads = [];
    private string? loaded;
    private string? pageFile;
    private long generation;
    private bool ready;
    private bool initialized;
    private bool released;
    private Task? releaseTask;

    public HtmlView(bool allowScripts, bool blockRemote, bool whiteBackground, IShellLauncher shell,
        HtmlPageFiles? pages = null, IUiDispatcher? dispatcher = null)
    {
        this.allowScripts = allowScripts;
        this.blockRemote = blockRemote;
        this.pages = pages ?? new HtmlPageFiles(log: error => App.Log(error));
        this.shell = shell;
        this.dispatcher = dispatcher;
        web.DefaultBackgroundColor = whiteBackground ? Colors.White : Colors.Transparent;
        Children.Add(web);
        Children.Add(shellError);
        initialization = InitializeAsync();
    }

    /// <summary>Starts a retained view load while keeping the task owned for release.</summary>
    public Task LoadAsync(string html) => StartLoad(html);

    private Task StartLoad(string html)
    {
        Task load;
        lock (gate)
        {
            if (released || html == loaded)
            {
                return Task.CompletedTask;
            }
            loaded = html;
            generation++;
            load = LoadCoreAsync(html, generation);
            loads.Add(load);
        }
        return load;
    }

    /// <summary>Stops initialization and writes, closes WebView2 on the UI dispatcher, then cleans files.</summary>
    public Task ReleaseAsync()
    {
        lock (gate)
        {
            if (releaseTask is not null)
            {
                return releaseTask;
            }
            released = true;
            generation++;
            releaseTask = ReleaseCoreAsync();
            return releaseTask;
        }
    }

    private async Task InitializeAsync()
    {
        try
        {
            await web.EnsureCoreWebView2Async();
            var core = web.CoreWebView2;
            if (App.Current.Options.Theme is { } theme)
            {
                core.Profile.PreferredColorScheme = theme == ApplicationTheme.Dark
                    ? CoreWebView2PreferredColorScheme.Dark
                    : CoreWebView2PreferredColorScheme.Light;
            }
            core.Settings.IsScriptEnabled = allowScripts;
            core.Settings.AreDevToolsEnabled = false;
            core.Settings.IsStatusBarEnabled = false;
            core.Settings.IsZoomControlEnabled = true;

            if (blockRemote)
            {
                core.AddWebResourceRequestedFilter("*", CoreWebView2WebResourceContext.All);
                core.WebResourceRequested += (_, e) =>
                {
                    if (e.Request.Uri.StartsWith("http:", StringComparison.OrdinalIgnoreCase) ||
                        e.Request.Uri.StartsWith("https:", StringComparison.OrdinalIgnoreCase))
                    {
                        e.Response = core.Environment.CreateWebResourceResponse(null, 403, "Blocked by Flock", "");
                    }
                };
            }

            core.NavigationStarting += (_, e) =>
            {
                if (!IsOwnPage(e.Uri))
                {
                    e.Cancel = true;
                    if (!shell.Open(e.Uri).Succeeded)
                    {
                        ShowShellError();
                    }
                }
            };
            core.NewWindowRequested += (_, e) =>
            {
                e.Handled = true;
                if (!shell.Open(e.Uri).Succeeded)
                {
                    ShowShellError();
                }
            };

            lock (gate)
            {
                initialized = true;
                ready = !released;
            }
        }
        catch (Exception error)
        {
            if (!IsReleased())
            {
                shellError.Message = Strings.App_NoWebView;
                shellError.IsOpen = true;
                App.Log(error);
            }
        }
    }

    private async Task LoadCoreAsync(string html, long identity)
    {
        try
        {
            await initialization;
            if (!IsCurrent(identity))
            {
                return;
            }
            if (html.Length <= MaxInlineLength)
            {
                var previous = pageFile;
                var navigated = await DispatchAsync(() =>
                {
                    if (!IsCurrent(identity))
                    {
                        return false;
                    }
                    pageFile = null;
                    web.NavigateToString(html);
                    return true;
                });
                if (navigated && previous is not null)
                {
                    await pages.ReleasePageAsync(previous);
                }
                return;
            }

            var file = await pages.WriteAsync(html, LifetimeToken());
            var previousFile = pageFile;
            var accepted = await DispatchAsync(() =>
            {
                if (!IsCurrent(identity))
                {
                    return false;
                }
                pageFile = file;
                web.Source = new Uri(file);
                return true;
            });
            if (accepted)
            {
                if (previousFile is not null)
                {
                    await pages.ReleasePageAsync(previousFile);
                }
            }
            else
            {
                await pages.ReleasePageAsync(file);
            }
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception error)
        {
            if (!IsReleased())
            {
                await ShowHtmlErrorAsync(Strings.App_CannotShowMessage);
                App.Log(error);
            }
        }
    }

    private async Task ReleaseCoreAsync()
    {
        try
        {
            await initialization;
            Task[] current;
            lock (gate)
            {
                current = [.. loads];
            }
            if (current.Length > 0)
            {
                await Task.WhenAll(current);
            }
            await DispatchAsync(() =>
            {
                if (initialized)
                {
                    web.Close();
                    initialized = false;
                    ready = false;
                }
                return true;
            });
        }
        finally
        {
            await pages.ReleaseAsync();
        }
    }

    private CancellationToken LifetimeToken()
    {
        lock (gate)
        {
            return released ? new CancellationToken(canceled: true) : CancellationToken.None;
        }
    }

    private bool IsCurrent(long identity)
    {
        lock (gate)
        {
            return !released && ready && identity == generation;
        }
    }

    private bool IsReleased()
    {
        lock (gate)
        {
            return released;
        }
    }

    private bool IsOwnPage(string uri)
    {
        lock (gate)
        {
            return uri.StartsWith("data:", StringComparison.OrdinalIgnoreCase) ||
                uri.StartsWith("about:", StringComparison.OrdinalIgnoreCase) ||
                (pageFile is not null && uri.Equals(new Uri(pageFile).AbsoluteUri, StringComparison.OrdinalIgnoreCase));
        }
    }

    private async Task<T> DispatchAsync<T>(Func<T> callback)
    {
        if (dispatcher is null)
        {
            return callback();
        }
        T result = default!;
        await dispatcher.InvokeAsync(() => result = callback());
        return result;
    }

    private void ShowShellError()
    {
        if (!IsReleased())
        {
            shellError.Message = Strings.App_CouldNotOpenLink;
            shellError.IsOpen = true;
        }
    }

    private Task ShowHtmlErrorAsync(string message) => DispatchAsync(() =>
    {
        if (!IsReleased())
        {
            shellError.Message = message;
            shellError.IsOpen = true;
        }
        return true;
    });
}

using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Sites;

namespace Flock.Tray.ViewModels;

/// <summary>
///     The output of a server Flock runs (a Node.js site's dev server, a site's Reverb server, a
///     proxy's command): the last lines of its log, read every second while the server runs, with
///     Clear and Open Log. The Sites pane keeps one for whichever details show output, so the text
///     survives the status updates.
/// </summary>
internal sealed partial class OutputViewModel(SitesService sites, Action<string> openLog) : ObservableObject
{
    public static string Empty => Strings.Sites_NoOutputYet;

    private string text = "";
    private bool live;
    private CancellationTokenSource? polling;

    /// <summary>The log shown, a name for /v1/logs/services/{name}.</summary>
    public string? Log { get; private set; }

    public string Text
    {
        get => text;
        private set => SetProperty(ref text, value);
    }

    /// <summary>Whether it's read every second.</summary>
    public bool IsPolling => polling is not null;

    /// <summary>Shows a log, and reads it now, and every second while live (the server starts or runs).</summary>
    public void Show(string log, bool isLive)
    {
        var changed = log != Log;
        Log = log;
        if (changed)
        {
            Text = "";
        }
        if (changed || isLive != live || polling is null)
        {
            live = isLive;
            Restart();
        }
    }

    /// <summary>Stops reading, while the pane is hidden or shows something without output.</summary>
    public void Stop()
    {
        polling?.Cancel();
        polling = null;
    }

    private void Restart()
    {
        Stop();
        polling = new CancellationTokenSource();
        _ = PollAsync(polling.Token);
    }

    private async Task PollAsync(CancellationToken token)
    {
        try
        {
            do
            {
                await ReadAsync(token);
                if (!live)
                {
                    return;
                }
                await Task.Delay(TimeSpan.FromSeconds(1), token);
            } while (!token.IsCancellationRequested);
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception error)
        {
            App.Log(error);
        }
    }

    private async Task ReadAsync(CancellationToken token)
    {
        if (Log is not { } name)
        {
            return;
        }
        var output = await sites.ServiceLogAsync(name, token);
        if (token.IsCancellationRequested || name != Log)
        {
            return;
        }
        var trimmed = (output ?? "").TrimEnd();
        Text = trimmed.Length == 0 ? Empty : trimmed;
    }

    [RelayCommand(AllowConcurrentExecutions = true)]
    private async Task ClearAsync()
    {
        // Only for the log still shown: another may have been selected while it was cleared.
        if (Log is { } name && await sites.ClearServiceLogAsync(name) && name == Log)
        {
            Text = Empty;
            // A read started before the clear would bring the old output back: read again instead.
            if (polling is not null)
            {
                Restart();
            }
        }
    }

    [RelayCommand]
    private void OpenLog()
    {
        if (Log is { } name)
        {
            openLog(name);
        }
    }
}

namespace Flock.Tray.Settings;

/// <summary>
///     A PHP site's Laravel Reverb server: turned on in its details, Flock runs php artisan
///     reverb:start and serves it at wss://&lt;domain&gt;/app (see ReverbSectionViewModel).
/// </summary>
internal sealed partial class SitesPane
{
    // Sites whose Reverb restart is in flight.
    private readonly HashSet<string> restartingReverb = [];

    private async Task RestartReverb(string site)
    {
        restartingReverb.Add(site);
        RefreshDetail();
        try
        {
            await Model.Sites.RestartReverbAsync(site);
        }
        finally
        {
            restartingReverb.Remove(site);
            RefreshDetail();
        }
    }
}

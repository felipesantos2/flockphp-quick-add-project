using System.Globalization;
using System.Xml.Linq;

namespace Flock.Kit;

/// <summary>
/// The app's update feed: a Sparkle appcast (the macOS app reads it with Sparkle) published in the
/// "updates" release. Every release has an item per OS; Windows items carry the installer's
/// SHA-256 (flock:sha256) and the oldest Windows version it runs on.
/// </summary>
public static class UpdateFeed
{
    public const string Url = "https://github.com/flock-php/flock/releases/download/updates/appcast.xml";

    private static readonly XNamespace Sparkle = "http://www.andymatuschak.org/xml-namespaces/sparkle";
    private static readonly XNamespace Flock = "https://flockphp.com/xml-namespaces/update";

    /// <summary>One release for one OS.</summary>
    /// <param name="Channel">"beta" for betas; empty for stable releases.</param>
    /// <param name="MinimumSystemVersion">The oldest OS version it runs on; null when any.</param>
    /// <param name="Notes">The release notes, as HTML.</param>
    /// <param name="NotesUrl">The release's page on GitHub.</param>
    public sealed record Item(
        AppVersion Version,
        string Channel,
        string Os,
        string Url,
        long Length,
        string Sha256,
        Version? MinimumSystemVersion,
        string Notes,
        string? NotesUrl);

    /// <summary>The feed's items with a version and a download; others are skipped. Throws FormatException for a feed that isn't XML.</summary>
    public static List<Item> Parse(string xml)
    {
        XDocument document;
        try
        {
            document = XDocument.Parse(xml);
        }
        catch (System.Xml.XmlException e)
        {
            throw new FormatException(Localization.Format(Strings.Updates_Invalid, e.Message), e);
        }

        var items = new List<Item>();
        foreach (var item in document.Descendants("item"))
        {
            var enclosure = item.Element("enclosure");
            var version = AppVersion.Parse(item.Element(Sparkle + "shortVersionString")?.Value);
            var url = enclosure?.Attribute("url")?.Value;
            if (enclosure is null || version is null || string.IsNullOrEmpty(url))
            {
                continue;
            }
            long.TryParse(enclosure.Attribute("length")?.Value, NumberStyles.None, CultureInfo.InvariantCulture, out var length);
            System.Version.TryParse(item.Element(Sparkle + "minimumSystemVersion")?.Value?.Trim(), out var minimum);
            items.Add(new Item(
                version,
                item.Element(Sparkle + "channel")?.Value.Trim() ?? "",
                // Sparkle's default for an enclosure without an OS.
                enclosure.Attribute(Sparkle + "os")?.Value.Trim() ?? "macos",
                url,
                length,
                enclosure.Attribute(Flock + "sha256")?.Value.Trim().ToLowerInvariant() ?? "",
                minimum,
                item.Element("description")?.Value.Trim() ?? "",
                item.Element(Sparkle + "fullReleaseNotesLink")?.Value.Trim()));
        }
        return items;
    }

    /// <summary>Whether a daemon channel ("stable", "beta", "experimental") takes beta versions of the app.</summary>
    public static bool TakesBetas(string? channel) => channel is "beta" or "experimental";

    /// <summary>
    /// The update for this app: the newest Windows item the channel allows that runs on this
    /// Windows version, when it is newer than the running version. A beta takes betas on any
    /// channel, until the release it leads to. Null for a development build.
    /// </summary>
    public static Item? Pick(IEnumerable<Item> items, AppVersion? current, bool beta, Version osVersion)
    {
        if (current is null)
        {
            return null;
        }
        beta |= current.IsBeta;
        var best = items
            .Where(i => i.Os == "windows" && (i.Channel.Length == 0 || (beta && i.Channel == "beta")))
            .Where(i => i.MinimumSystemVersion is null || i.MinimumSystemVersion <= osVersion)
            .MaxBy(i => i.Version);
        return best is not null && best.Version > current ? best : null;
    }
}
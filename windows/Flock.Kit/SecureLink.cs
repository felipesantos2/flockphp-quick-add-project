namespace Flock.Kit;

/// <summary>
/// A link from the page Flock serves for a linked site that isn't secured yet, asking to serve the
/// site over HTTPS: <c>flock://secure/&lt;site&gt;</c>, e.g. <c>flock://secure/watchtower</c>.
///
/// Any web page can open such a link, so like an InstallLink it changes nothing by itself: the
/// app asks the user to confirm (see Resolve).
/// </summary>
/// <param name="Site">The site's name ("watchtower").</param>
public sealed record SecureLink(string Site)
{
    /// <summary>The link in a command line or message, or null when it isn't a valid secure link.</summary>
    public static SecureLink? Parse(string? text)
    {
        if (string.IsNullOrEmpty(text) || text.Length > 200
            || !Uri.TryCreate(text, UriKind.Absolute, out var uri)
            || !uri.Scheme.Equals(InstallLink.Scheme, StringComparison.OrdinalIgnoreCase)
            || !uri.Host.Equals("secure", StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }
        var parts = Uri.UnescapeDataString(uri.AbsolutePath).Split('/', StringSplitOptions.RemoveEmptyEntries);
        if (parts.Length != 1 || !IsSafe(parts[0]))
        {
            return null;
        }
        return new SecureLink(parts[0].ToLowerInvariant());
    }

    /// <summary>Whether text is any flock:// link the app handles.</summary>
    public static bool IsFlockLink(string? text) =>
        InstallLink.Parse(text) is not null || Parse(text) is not null || UpdateLink.IsUpdateLink(text);

    /// <summary>Letters, digits, dots and dashes only, so nothing odd ends up in a dialog or a request.</summary>
    private static bool IsSafe(string part) =>
        part.Length is > 0 and <= 63 && part.All(c => char.IsAsciiLetterOrDigit(c) || c is '.' or '-');

    /// <summary>What the link asks for, measured against the linked sites. Null while they are loading.</summary>
    public SecureLinkResolution? Resolve(IReadOnlyList<Status.Site>? sites)
    {
        if (sites is null)
        {
            return null;
        }
        var site = sites.FirstOrDefault(s => s.Name.Equals(Site, StringComparison.OrdinalIgnoreCase));
        if (site is null)
        {
            return SecureLinkResolution.Unknown(Site);
        }
        return site.Secured ? SecureLinkResolution.AlreadySecured(site) : SecureLinkResolution.Secure(site);
    }
}

/// <summary>What the app tells the user about a secure link: a question to confirm, or why there is nothing to do.</summary>
/// <param name="Site">Set when there is a site to secure: its name.</param>
public sealed record SecureLinkResolution(string Title, string Message, string? Site = null)
{
    public static SecureLinkResolution Secure(Status.Site site) => new(
        Localization.Format(Strings.Links_Secure_SecureTitle, site.Name),
        Localization.Format(Strings.Links_Secure_SecureMessage, site.Domain),
        site.Name);

    public static SecureLinkResolution AlreadySecured(Status.Site site) =>
        new(Localization.Format(Strings.Links_Secure_AlreadySecuredTitle, site.Name), Localization.Format(Strings.Links_Secure_AlreadySecuredMessage, site.Domain));

    public static SecureLinkResolution Unknown(string name) =>
        new(Localization.Format(Strings.Links_Secure_UnknownTitle, name), Strings.Links_Secure_UnknownMessage);
}
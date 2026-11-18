namespace Flock.Kit;

/// <summary>
/// A link from the website or the docs that asks to install a service:
/// <c>flock://install/&lt;service&gt;</c> or <c>flock://install/&lt;service&gt;/&lt;version line&gt;</c>,
/// e.g. <c>flock://install/mysql/8.4</c> or <c>flock://install/postgres/18</c>. <c>flock://install/php/8.5</c>
/// (from the page served for a site whose PHP is missing) and <c>flock://install/node/24</c> ask for
/// a PHP or Node.js version instead.
///
/// Any web page can open such a link, so it never installs anything by itself: the app shows what
/// it would install and waits for the user to confirm (see Resolve, ResolvePhp and ResolveNode).
/// </summary>
/// <param name="Service">The service id ("mysql"), Php or Node.</param>
/// <param name="Line">The version line ("8.4", a PostgreSQL or Node.js major); null for the newest.</param>
public sealed record InstallLink(string Service, string? Line = null)
{
    public const string Scheme = "flock";

    /// <summary>The Service of a link that asks for a PHP minor, which the PHP pane installs.</summary>
    public const string Php = "php";

    /// <summary>The Service of a link that asks for a Node.js major, which the Node pane installs.</summary>
    public const string Node = "node";

    /// <summary>The link in a command line or message, or null when it isn't a valid install link.</summary>
    public static InstallLink? Parse(string? text)
    {
        if (string.IsNullOrEmpty(text) || text.Length > 200
            || !Uri.TryCreate(text, UriKind.Absolute, out var uri)
            || !uri.Scheme.Equals(Scheme, StringComparison.OrdinalIgnoreCase)
            || !uri.Host.Equals("install", StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }
        var parts = Uri.UnescapeDataString(uri.AbsolutePath).Split('/', StringSplitOptions.RemoveEmptyEntries);
        if (parts.Length is < 1 or > 2 || !IsSafe(parts[0], 32) || (parts.Length == 2 && !IsSafe(parts[1], 16)))
        {
            return null;
        }
        return new InstallLink(parts[0].ToLowerInvariant(), parts.Length == 2 ? parts[1] : null);
    }

    /// <summary>Letters, digits, dots and dashes only, so nothing odd ends up in a dialog or a request.</summary>
    private static bool IsSafe(string part, int maxLength) =>
        part.Length > 0 && part.Length <= maxLength && part.All(c => char.IsAsciiLetterOrDigit(c) || c is '.' or '-');

    /// <summary>
    /// What the link asks for, measured against what this Flock offers and has installed. Null while
    /// the services and versions are still loading.
    /// </summary>
    public InstallLinkResolution? Resolve(IReadOnlyList<Service> services, ServiceCatalog? catalog, PostgresVersions? postgres)
    {
        if (services.Count == 0)
        {
            return null;
        }
        var service = services.FirstOrDefault(s => s.Id == Service);
        if (service is null)
        {
            return InstallLinkResolution.Unknown(Service);
        }
        if (!service.Available)
        {
            return InstallLinkResolution.Unavailable(service.Name, service.Unavailable);
        }

        if (service.Id == "postgres")
        {
            if (postgres is null)
            {
                return null;
            }
            var major = Line is null ? postgres.Majors.FirstOrDefault() : postgres.Majors.FirstOrDefault(m => m.Major == Line);
            if (major is null)
            {
                return InstallLinkResolution.UnknownVersion(service.Name, Line ?? "");
            }
            return major.IsInstalled
                ? InstallLinkResolution.AlreadyInstalled($"PostgreSQL {major.Major}")
                : InstallLinkResolution.Install(new InstallLinkTarget(service.Id, service.Name, major.Major, major.Major, major.Latest));
        }

        if (catalog is null)
        {
            return null;
        }
        var entry = catalog[service.Id];
        if (entry is null)
        {
            return InstallLinkResolution.Unknown(service.Name);
        }
        var target = Line is null ? entry.Lines.FirstOrDefault() : entry.Lines.FirstOrDefault(l => l.Id == Line);
        if (target is null)
        {
            return InstallLinkResolution.UnknownVersion(entry.Name, Line ?? "");
        }
        if (target.Installed)
        {
            return InstallLinkResolution.AlreadyInstalled($"{entry.Name} {target.Label}");
        }
        return InstallLinkResolution.Install(new InstallLinkTarget(entry.Id, entry.Name, target.Id, target.Label, target.Latest));
    }

    /// <summary>
    /// What a php link asks for, measured against the PHP versions. Without a version, the newest
    /// one that isn't a pre-release. Null while the versions are still loading.
    /// </summary>
    public InstallLinkResolution? ResolvePhp(PhpCatalog? catalog)
    {
        if (catalog is null)
        {
            return null;
        }
        var release = Line is null
            ? catalog.Versions.Where(v => !v.Prerelease && v.Latest.Length > 0).MaxBy(v => Numeric(v.Minor))
            : catalog.Versions.FirstOrDefault(v => v.Minor == Line);
        if (release is null || (!release.IsInstalled && release.Latest.Length == 0))
        {
            return InstallLinkResolution.UnknownVersion("PHP", Line ?? "", Strings.Links_Install_NoBuild);
        }
        var name = $"PHP {release.Minor}";
        if (release.IsInstalled)
        {
            return InstallLinkResolution.AlreadyInstalled(name, "PHP");
        }
        if (release.Job?.IsRunning == true)
        {
            return InstallLinkResolution.Installing(name, "PHP");
        }
        return InstallLinkResolution.Install(new InstallLinkTarget(Php, "PHP", release.Minor, release.Minor, release.Latest),
            Strings.Links_Install_PurposePhp);
    }

    /// <summary>
    /// What a node link asks for, measured against the Node.js lines. Without a version, the newest
    /// line. Null while the lines are still loading.
    /// </summary>
    /// <param name="installing">Majors that are being installed already (the first run's default, one started in the Node pane).</param>
    public InstallLinkResolution? ResolveNode(NodeCatalog? catalog, IReadOnlyCollection<string>? installing = null)
    {
        if (catalog is null)
        {
            return null;
        }
        var release = Line is null
            ? catalog.Versions.Where(v => v.Latest.Length > 0).MaxBy(v => Numeric(v.Major))
            : catalog.Versions.FirstOrDefault(v => v.Major == Line);
        if (release is null || (!release.IsInstalled && release.Latest.Length == 0))
        {
            return InstallLinkResolution.UnknownVersion("Node.js", Line ?? "", Strings.Links_Install_NoBuild);
        }
        var name = $"Node.js {release.Major}";
        if (release.IsInstalled)
        {
            return InstallLinkResolution.AlreadyInstalled(name, "Node");
        }
        if (installing?.Contains(release.Major) == true)
        {
            return InstallLinkResolution.Installing(name, "Node");
        }
        return InstallLinkResolution.Install(new InstallLinkTarget(Node, "Node.js", release.Major, release.Major, release.Latest),
            Strings.Links_Install_PurposeNode);
    }

    /// <summary>Versions compare by their numbers: "8.10" is newer than "8.9".</summary>
    private static Version Numeric(string version) =>
        Version.TryParse(version.Contains('.') ? version : version + ".0", out var parsed) ? parsed : new Version();
}

/// <summary>What an install link would install once the user confirms.</summary>
/// <param name="Line">The line to pass to the daemon ("8.4", "18").</param>
/// <param name="Label">How the line reads ("8.4 LTS").</param>
/// <param name="Version">The release that gets installed; empty when not known yet.</param>
public sealed record InstallLinkTarget(string Service, string Name, string Line, string Label, string Version);

/// <summary>What the app tells the user about an install link: a question to confirm, or why there is nothing to install.</summary>
/// <param name="Target">Set when there is something to install.</param>
public sealed record InstallLinkResolution(string Title, string Message, InstallLinkTarget? Target = null)
{
    /// <param name="purpose">What it is for once installed; a service that runs for the sites when null.</param>
    public static InstallLinkResolution Install(InstallLinkTarget target, string? purpose = null) => new(
        Localization.Format(Strings.Links_Install_InstallTitle, target.Name, target.Label),
        Localization.Format(Strings.Links_Install_InstallMessage, target.Name, target.Version.Length > 0 ? target.Version : target.Label,
            purpose ?? Strings.Links_Install_PurposeService),
        target);

    /// <param name="pane">The Settings pane that lists it; Services when null.</param>
    public static InstallLinkResolution AlreadyInstalled(string name, string? pane = null) =>
        new(Localization.Format(Strings.Links_Install_AlreadyInstalledTitle, name), Localization.Format(Strings.Links_Install_AlreadyInstalledMessage, pane ?? Strings.Settings_Pane_Services));

    public static InstallLinkResolution Installing(string name, string pane) =>
        new(Localization.Format(Strings.Links_Install_InstallingTitle, name), Localization.Format(Strings.Links_Install_InstallingMessage, pane));

    public static InstallLinkResolution Unavailable(string name, string reason) =>
        new(Localization.Format(Strings.Links_Install_UnavailableTitle, name), reason.Length > 0 ? reason : Strings.Links_Install_UnavailableMessage);

    public static InstallLinkResolution Unknown(string name) =>
        new(Localization.Format(Strings.Links_Install_UnknownTitle, name), Strings.Links_Install_NewerFlock);

    /// <param name="message">Why; that the link may be meant for a newer Flock when null.</param>
    public static InstallLinkResolution UnknownVersion(string name, string line, string? message = null) =>
        new(Localization.Format(Strings.Links_Install_UnknownTitle, $"{name} {line}".TrimEnd()), message ?? Strings.Links_Install_NewerFlock);
}
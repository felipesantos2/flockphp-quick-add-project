using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Sites;
using IOPath = System.IO.Path;

namespace Flock.Tray.ViewModels;

/// <summary>
///     The selected site's details: its address, domains and document root, its PHP and Node.js
///     versions and what serves it, Reverb, Laravel Boost and its worktrees. Each version picker offers
///     the project's pinned version (or the default) first, then the installed versions, which isolate
///     the site on this machine; the document root picker works the same way. The "…" menu pins a
///     version or the document root into the project or removes it again.
/// </summary>
internal sealed partial class SiteDetailViewModel : DetailViewModel
{
    private readonly ISiteDetailHost host;
    private Status.Site site = new();
    private Status? status;
    private int typeIndex;
    private int typeShown;
    private bool securedShown;
    private List<string> rootFolders = [];
    private int rootIndex;
    private int rootShown;

    public SiteDetailViewModel(ISiteDetailHost host, OutputViewModel output)
    {
        this.host = host;
        Php = new VersionPickerViewModel(Strings.Common_Php);
        Node = new VersionPickerViewModel(Strings.Common_Node);
        Share = new ShareSectionViewModel(host);
        Server = new SiteServerSectionViewModel(host);
        Reverb = new ReverbSectionViewModel(host, output);
        DevServer = new DevServerSectionViewModel(host, output);
    }

    public bool IsShown { get; private set; }
    public Status.Site Site => site;
    public SiteRowViewModel? Row { get; private set; }

    // Header: name, address and folder, and the site's actions.
    public string Name => site.Name;
    public bool IsProjectFolder => site.Parked;
    public string Url => site.Url ?? "";
    public bool HasUrl => site.Url is not null;
    public string Path => site.Path;
    public string OpenLabel => Localization.Format(Strings.Common_OpenTarget, site.Url ?? site.Domain);
    public bool IsPhp => !site.IsNode;
    public bool IsNode => site.IsNode;
    public bool CanRemove => !site.Parked;
    public string Problem => site.Problem ?? "";

    // The "…" menu: pin and unpin items appear only when there is something to pin or unpin.
    public bool ShowsPinPhp => site.Php.Length > 0 && !(site.PhpSource == Status.VersionSource.Project && site.PhpPinnedHere);
    public string PinPhpText => Localization.Format(Strings.Sites_PinPhpItem, site.Php);
    public bool ShowsPinNode => NodeMajor.Length > 0 && !(site.NodeSource == Status.VersionSource.Project && site.NodePinnedHere);
    public string PinNodeText => Localization.Format(Strings.Sites_PinNodeItem, NodeMajor);
    public bool ShowsPinRoot => !site.IsNode && site.RootState is "" or Status.DocumentRootState.Empty && site.RootSource != Status.VersionSource.Project;
    public string PinRootText => Localization.Format(Strings.Sites_PinRootItem, RootLabel(site.Root));
    public bool ShowsUnpinPhp => site.PhpPinnedHere;
    public string UnpinPhpText => Localization.Format(Strings.Sites_UnpinPhpItem, IOPath.GetFileName(site.PhpFile));
    public bool ShowsUnpinNode => site.NodePinnedHere;
    public string UnpinNodeText => Localization.Format(Strings.Sites_UnpinNodeItem, IOPath.GetFileName(site.NodeFile));
    public bool ShowsUnpinRoot => site.RootPinnedHere;
    private bool HasPins => ShowsPinPhp || ShowsPinNode || ShowsPinRoot;
    private bool HasUnpins => ShowsUnpinPhp || ShowsUnpinNode || ShowsUnpinRoot;
    public bool ShowsMenuSeparator => HasPins && HasUnpins;
    public bool HasMenu => HasPins || HasUnpins;

    // Site: its type, HTTPS, the domains besides its own and the document root.
    public int TypeIndex
    {
        get => typeShown;
        set
        {
            if (Refreshing || value < 0)
            {
                return;
            }
            typeShown = value;
            if (value == typeIndex)
            {
                return;
            }
            var change = value == 1 ? Status.SiteType.Node : Status.SiteType.Php;
            var name = site.Name;
            host.Start(name, () => host.UpdateSiteAsync(name, new SiteChange(Type: change), null), false);
        }
    }

    public bool Secured
    {
        get => securedShown;
        set
        {
            if (Refreshing || value == securedShown)
            {
                return;
            }
            securedShown = value;
            var (name, row) = (site.Name, Row);
            host.Start(name, () => host.UpdateSiteAsync(name, new SiteChange(Secured: value), () => row?.SetDraftSecured(value)), false);
        }
    }

    public IReadOnlyList<DomainItemViewModel> Domains { get; private set; } = [];
    public bool HasDomains => Domains.Count > 0;
    public bool ShowsWildcardNote => status?.Dns is { Wildcard: false, WildcardProblem.Length: > 0 };
    public string WildcardNote => Localization.Format(Strings.Sites_SubdomainsOnlyHere, status?.Dns.WildcardProblem);

    public IReadOnlyList<string> RootItems { get; private set; } = [];
    public int RootIndex
    {
        get => rootShown < RootItems.Count ? rootShown : -1;
        set
        {
            if (Refreshing || value < 0 || value == rootIndex)
            {
                return;
            }
            var current = site;
            if (value == RootItems.Count - 1)
            {
                // Choose Folder…: the picker shows the root until one is chosen.
                rootShown = rootIndex;
                host.Post(() => RefreshOne(nameof(RootIndex)));
                host.Start(current.Name, () => host.ChooseRootAsync(current));
                return;
            }
            rootShown = value;
            var root = value == 0 ? "" : rootFolders[value - 1];
            host.Start(current.Name, () => host.Sites.UpdateAsync(current.Name, new SiteChange(Root: root)));
        }
    }

    public string RootTooltip => site.RootSource switch
    {
        Status.VersionSource.Isolated => Localization.Format(Strings.Sites_RootIsolated, RootLabel(site.Root)),
        Status.VersionSource.Project => Localization.Format(Strings.Sites_RootPinned, RootLabel(site.Root)),
        _ => Strings.Sites_RootDefault
    };

    /// <summary>Why the site isn't served from its root, and a root set here over the project's.</summary>
    public IReadOnlyList<HintViewModel> RootHints { get; private set; } = [];
    public bool HasRootHints => RootHints.Count > 0;
    public string RootEmptyNote => site.RootState == Status.DocumentRootState.Empty
        ? Localization.Format(Strings.Sites_RootNoIndex, RootLabel(site.Root))
        : "";

    // Versions: PHP and Node.js (a Node.js app has no PHP), with what pins or isolates them.
    public VersionPickerViewModel Php { get; }
    public VersionPickerViewModel Node { get; }
    public IReadOnlyList<HintViewModel> VersionHints { get; private set; } = [];
    public bool HasVersionHints => VersionHints.Count > 0;
    public string VersionsNote => site.IsNode
        ? Strings.Sites_VersionsFootnoteNode
        : Strings.Sites_VersionsFootnote;

    public ShareSectionViewModel Share { get; }
    public SiteServerSectionViewModel Server { get; }
    public DevServerSectionViewModel DevServer { get; }
    public ReverbSectionViewModel Reverb { get; }

    // Laravel Boost: the project uses it, and Flock's guideline is missing from it or outdated.
    public bool ShowsBoost => !site.IsNode && site.Boost is Status.BoostState.Missing or Status.BoostState.Outdated;
    private bool BoostMissing => site.Boost == Status.BoostState.Missing;
    public string BoostText => BoostMissing
        ? Strings.Sites_BoostMissing
        : Strings.Sites_BoostOutdated;
    public string BoostAction => BoostMissing ? Strings.Sites_BoostAdd : Strings.Sites_BoostUpdate;

    // Worktrees: each selects it, and New Worktree.
    public IReadOnlyList<WorktreeItemViewModel> Worktrees { get; private set; } = [];
    public bool HasNoWorktrees => Worktrees.Count == 0;
    public string WorktreesNote => Localization.Format(Strings.Worktrees_Explanation, site.Domain);

    /// <summary>The output shown: the dev server's of a Node.js site, else Reverb's.</summary>
    public OutputSource? Output => site.IsNode ? DevServer.Output : Reverb.Output;

    private string NodeMajor => Major(site.Node ?? "");

    public void Hide()
    {
        if (IsShown)
        {
            IsShown = false;
            OnPropertyChanged(nameof(IsShown));
        }
    }

    /// <summary>Shows a site (row keeps the values picked here until the daemon has them).</summary>
    public void Update(Status.Site value, SiteRowViewModel? row, FlockConfig? config, Status? current, bool restarting, bool restartingReverb)
    {
        site = value;
        Row = row;
        status = current;
        IsShown = true;
        typeIndex = value.IsNode ? 1 : 0;
        typeShown = typeIndex;
        securedShown = row?.Secured ?? value.Secured;

        Domains = value.Domains.Select(domain => new DomainItemViewModel(host, value, domain)).ToList();

        var follow = value.RootPinnedHere ? Localization.Format(Strings.Sites_UseFileVersion, ".flockrc", RootLabel(value.RootPinned))
            : value.RootSource != Status.VersionSource.Default ? Strings.Sites_RootAutomatic
            : value.RootState == Status.DocumentRootState.NotFound ? Strings.Sites_RootAutomaticNone
            : Localization.Format(Strings.Sites_RootAutomaticFound, RootLabel(value.Root));
        rootFolders = value.RootSuggestions.ToList();
        if (value.RootSource == Status.VersionSource.Isolated && !rootFolders.Contains(value.Root))
        {
            rootFolders.Add(value.Root);
        }
        RootItems = Same(RootItems, [follow, .. rootFolders.Select(RootLabel), Strings.Sites_ChooseFolder]);
        rootIndex = value.RootSource == Status.VersionSource.Isolated ? rootFolders.IndexOf(value.Root) + 1 : 0;
        rootShown = rootIndex;
        RootHints = RootHintsOf(value);

        Php.Update(row?.Php ?? value.Php, value.PhpSource, value.PhpPinned, value.PhpFile, config?.DefaultPhp ?? "",
            config?.Php.Select(p => p.Version).ToList() ?? [],
            version => SetVersion(new SiteChange(Php: version), () => row?.SetDraftPhp(version)));
        Node.Update(row?.Node ?? Major(value.Node ?? ""), value.NodeSource, Major(value.NodePinned), value.NodeFile, config?.DefaultNode ?? "",
            config?.NodeMajors ?? [],
            version => SetVersion(new SiteChange(Node: version), () => row?.SetDraftNode(version)));
        VersionHints = VersionHintsOf(value);

        Share.Update(value);
        Server.Update(value, row, restarting);
        DevServer.Update(value);
        Reverb.Update(value, row, restartingReverb);
        Worktrees = (current?.WorktreesOf(value.Name) ?? []).Select(worktree => new WorktreeItemViewModel(host, worktree)).ToList();

        Refresh(nameof(RootItems));
    }

    private void SetVersion(SiteChange change, Func<Action?> draft)
    {
        var name = site.Name;
        host.Start(name, () => host.UpdateSiteAsync(name, change, draft), false);
    }

    private List<HintViewModel> RootHintsOf(Status.Site value)
    {
        if (value.RootState == Status.DocumentRootState.NotFound)
        {
            return [new HintViewModel(
                Strings.Sites_RootNotFound,
                null, null)];
        }
        if (value.RootState == Status.DocumentRootState.Missing)
        {
            return [new HintViewModel(Localization.Format(Strings.Sites_RootMissing, value.Root), null, null)];
        }
        if (value.RootState != Status.DocumentRootState.Empty && value.RootSource == Status.VersionSource.Isolated && value.RootPinnedHere)
        {
            return [new HintViewModel(Localization.Format(Strings.Sites_RootShadowed, RootLabel(value.Root), RootLabel(value.RootPinned)),
                Localization.Format(Strings.Sites_UseFile, ".flockrc"), () => host.Start(value.Name, () => host.Sites.UpdateAsync(value.Name, new SiteChange(Root: ""))))];
        }
        return [];
    }

    /// <summary>Notes when versions disagree: an isolation over the project's pin, .flockrc over .nvmrc.</summary>
    private List<HintViewModel> VersionHintsOf(Status.Site value)
    {
        var hints = new List<HintViewModel>();
        if (value.PhpSource == Status.VersionSource.Isolated && value.PhpPinned.Length > 0 && value.PhpPinned != value.Php)
        {
            var file = IOPath.GetFileName(value.PhpFile);
            hints.Add(new HintViewModel(Localization.Format(Strings.Sites_VersionShadowed, $"PHP {value.Php}", value.PhpPinned, file),
                Localization.Format(Strings.Sites_UseFile, file), () => host.Start(value.Name, () => host.Sites.UpdateAsync(value.Name, new SiteChange(Php: "")))));
        }
        if (value.NodeSource == Status.VersionSource.Isolated && value.NodePinned.Length > 0 && Major(value.NodePinned) != Major(value.Node ?? ""))
        {
            var file = IOPath.GetFileName(value.NodeFile);
            hints.Add(new HintViewModel(Localization.Format(Strings.Sites_VersionShadowed, $"Node {value.Node}", value.NodePinned, file),
                Localization.Format(Strings.Sites_UseFile, file), () => host.Start(value.Name, () => host.Sites.UpdateAsync(value.Name, new SiteChange(Node: "")))));
        }
        if (value.NodeShadowed.Length > 0)
        {
            hints.Add(new HintViewModel(Localization.Format(Strings.Sites_NodeWins, value.NodePinned, value.NodeShadowed), null, null));
        }
        return hints;
    }

    /// <summary>"src/www", or "Project folder" for ".".</summary>
    internal static string RootLabel(string root) => root == "." ? Strings.Sites_ProjectFolderBadge : root;

    [RelayCommand]
    private void Open() => host.Open(site);

    [RelayCommand]
    private void Reveal() => host.Reveal(site.Path);

    [RelayCommand]
    private void ShowExtensions()
    {
        var current = site;
        host.Start("extensions:" + current.Name, () => host.ShowExtensionsAsync(current));
    }

    [RelayCommand]
    private void Remove()
    {
        var current = site;
        host.Start(current.Name, () => host.RemoveSiteAsync(current));
    }

    [RelayCommand]
    private void PinPhp()
    {
        var current = site;
        host.Start(current.Name, () => host.PinAsync(current, "php", current.Php,
            Localization.Format(Strings.Sites_PinPhpDetails, current.Php, current.Path)));
    }

    [RelayCommand]
    private void PinNode()
    {
        var current = site;
        var major = Major(current.Node ?? "");
        var file = current.NodePinnedHere ? IOPath.GetFileName(current.NodeFile) : ".nvmrc";
        host.Start(current.Name, () => host.PinAsync(current, "node", major,
            Localization.Format(Strings.Sites_PinNodeDetails, major, file, current.Path)));
    }

    [RelayCommand]
    private void PinRoot()
    {
        var current = site;
        host.Start(current.Name, () => host.PinAsync(current, "root", current.Root,
            Localization.Format(Strings.Sites_PinRootDetails, current.Root, current.Path, RootLabel(current.Root))));
    }

    [RelayCommand]
    private void UnpinPhp()
    {
        var current = site;
        host.Start(current.Name, () => host.UnpinAsync(current, "php",
            Localization.Format(Strings.Sites_UnpinPhpDetails, current.PhpFile)));
    }

    [RelayCommand]
    private void UnpinNode()
    {
        var current = site;
        host.Start(current.Name, () => host.UnpinAsync(current, "node",
            Localization.Format(Strings.Sites_UnpinNodeDetails, current.Path)));
    }

    [RelayCommand]
    private void UnpinRoot()
    {
        var current = site;
        host.Start(current.Name, () => host.UnpinAsync(current, "root",
            Localization.Format(Strings.Sites_UnpinRootDetails, current.Path)));
    }

    [RelayCommand]
    private void AddDomain()
    {
        var current = site;
        host.Start(current.Name, () => host.AddDomainAsync(current));
    }

    [RelayCommand]
    private void AddBoostGuideline()
    {
        var (current, missing) = (site, BoostMissing);
        host.Start(current.Name, () => host.AddBoostGuidelineAsync(current, missing));
    }

    [RelayCommand]
    private void NewWorktree()
    {
        var current = site;
        host.Start(current.Name, () => host.NewWorktreeAsync(current));
    }
}

/// <summary>
///     A version picker: first the project's pinned version ("Use .nvmrc (Node 20)") or the default,
///     which follow the project; then the installed versions, which isolate the site.
/// </summary>
internal sealed class VersionPickerViewModel(string label) : DetailViewModel
{
    private List<string> versions = [];
    private int shown;
    private Action<string> pick = _ => { };

    public IReadOnlyList<string> Items { get; private set; } = [];
    public string Tooltip { get; private set; } = "";

    public int SelectedIndex
    {
        get => shown < Items.Count ? shown : -1;
        set
        {
            if (Refreshing || value < 0 || value == shown)
            {
                return;
            }
            shown = value;
            pick(value == 0 ? "" : versions[value - 1]);
        }
    }

    public void Update(string current, string source, string pinned, string file, string defaultVersion, List<string> installed, Action<string> onPick)
    {
        pick = onPick;
        // No default yet while the first run installs it.
        var follow = pinned.Length > 0 ? Localization.Format(Strings.Sites_UseFileVersion, Path.GetFileName(file), $"{label} {pinned}")
            : defaultVersion.Length > 0 ? Localization.Format(Strings.Sites_DefaultVersion, $"{label} {defaultVersion}")
            : Strings.Sites_Default;
        versions = installed.ToList();
        if (source == Status.VersionSource.Isolated && current.Length > 0 && !versions.Contains(current))
        {
            versions.Add(current); // isolated to a version that isn't installed (any more)
        }
        Items = Same(Items, [follow, .. versions.Select(version => $"{label} {version}")]);
        shown = source == Status.VersionSource.Isolated ? versions.IndexOf(current) + 1 : 0;
        Tooltip = source switch
        {
            Status.VersionSource.Isolated => Localization.Format(Strings.Sites_VersionIsolated, $"{label} {current}"),
            Status.VersionSource.Project => Localization.Format(Strings.Sites_VersionPinned, $"{label} {pinned}", Path.GetFileName(file)),
            _ => Localization.Format(Strings.Sites_VersionDefault, label)
        };
        Refresh(nameof(Items));
    }
}

/// <summary>One of a site's extra domains: its link (or why it isn't served) and Remove.</summary>
internal sealed partial class DomainItemViewModel(ISiteDetailHost host, Status.Site site, Status.SiteDomain domain)
{
    public string Problem => domain.Problem is { } problem ? $"{domain.Host}: {problem}" : "";
    public bool HasProblem => domain.Problem is not null;
    public bool IsLink => !HasProblem && domain.Url is { Length: > 0 };
    public bool IsText => !HasProblem && !IsLink;
    public string Host => domain.Host;
    public string LinkText => domain.Alias ? Localization.Format(Strings.Sites_Alias, domain.Host) : domain.Host;
    public string LinkTooltip => Localization.Format(Strings.Common_OpenTarget, domain.Url);
    public string RemoveLabel => Localization.Format(Strings.Common_RemoveTarget, domain.Host);

    [RelayCommand]
    private void Open()
    {
        if (domain.Url is { Length: > 0 } url)
        {
            host.OpenUrl(url);
        }
    }

    [RelayCommand]
    private void Remove() => host.Start(site.Name, () => host.RemoveDomainAsync(site, domain));
}

/// <summary>A worktree in its site's details: a link that selects it, and its setup or its own database.</summary>
internal sealed partial class WorktreeItemViewModel(ISiteDetailHost host, Status.Site worktree)
{
    public string Label => worktree.WorktreeLabel;
    public string Path => worktree.Path;
    public string Domain => worktree.Domain;
    public bool IsBusy => worktree.Worktree is { IsBusy: true };
    public string Step => worktree.Worktree is { Step.Length: > 0 } setup ? setup.Step : Strings.Worktrees_SettingUp;
    public bool IsFailed => worktree.Worktree is { IsBusy: false, IsFailed: true };
    public bool HasOwnDatabase => worktree.Worktree is { IsBusy: false, IsFailed: false, HasOwnDatabase: true };
    public string DatabaseLabel => worktree.Worktree is { DbMode.Length: > 0 } setup ? setup.DbMode : Strings.Worktrees_OwnDatabase;

    [RelayCommand]
    private void Select() => host.Select(worktree.Name);
}

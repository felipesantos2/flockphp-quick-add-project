using CommunityToolkit.Mvvm.Input;
using Flock.Kit;

namespace Flock.Tray.ViewModels;

/// <summary>
///     The selected worktree: its branch, folder and address, its site's settings (read-only, they are
///     changed on the site), a Node.js worktree's dev server, its database and its setup.
/// </summary>
internal sealed partial class WorktreeDetailViewModel : DetailViewModel
{
    // The database pickers' modes and their labels, in their order.
    internal static readonly string[] DatabaseModes = [Status.WorktreeDatabase.Shared, Status.WorktreeDatabase.Copy, Status.WorktreeDatabase.Fresh];
    internal static string[] DatabaseChoices => [Strings.Worktrees_DatabaseShared, Strings.Worktrees_DatabaseCopy, Strings.Worktrees_DatabaseFresh];

    private readonly ISiteDetailHost host;
    private Status.Site worktree = new();
    private bool hasParent;
    private int databaseIndex;
    private int databaseShown;

    public WorktreeDetailViewModel(ISiteDetailHost host, OutputViewModel output)
    {
        this.host = host;
        DevServer = new DevServerSectionViewModel(host, output);
    }

    public bool IsShown { get; private set; }

    // Header: branch, address and folder, and the worktree's actions.
    public string Label => worktree.WorktreeLabel;
    public string Url => worktree.Url ?? "";
    public bool HasUrl => worktree.Url is not null;
    public string Path => worktree.Path;
    public string OpenLabel => Localization.Format(Strings.Common_OpenTarget, worktree.Url ?? worktree.Domain);
    public string Problem => worktree.Problem ?? "";

    // Worktree: its branch, folder and site.
    public string Branch => worktree.Branch.Length > 0 ? worktree.Branch : Strings.Worktrees_DetachedHead;
    public string Domain => worktree.Domain;
    public string Parent => worktree.Parent ?? "";
    public bool HasParent => hasParent;
    public bool HasNoParent => !hasParent;

    // Settings: the site's, so they are changed there.
    public bool IsPhp => !worktree.IsNode;
    public bool IsNode => worktree.IsNode;
    public string PhpText => $"PHP {worktree.Php}";
    public string NodeText => worktree.Node is { Length: > 0 } node ? $"Node {node}" : Strings.Worktrees_None;
    public string HttpsText => worktree.Secured ? Strings.Worktrees_On : Strings.Worktrees_Off;
    public string SettingsNote => worktree.IsNode
        ? Localization.Format(Strings.Worktrees_UsesSiteSettingsNode, Parent)
        : Localization.Format(Strings.Worktrees_UsesSiteSettings, Parent);

    public DevServerSectionViewModel DevServer { get; }

    // Database: the site's or its own, and a picker that switches it.
    private Status.WorktreeSetup? Setup => worktree.Worktree;
    public string CurrentDatabase => Setup is { HasOwnDatabase: true } setup ? setup.Database : Strings.Worktrees_DatabaseShared;
    public IReadOnlyList<string> DatabaseItems { get; } = DatabaseChoices;

    public int DatabaseIndex
    {
        get => databaseShown;
        set
        {
            if (Refreshing || value < 0 || value == databaseIndex)
            {
                return;
            }
            var mode = DatabaseModes[value];
            // Shown again once the daemon has switched it; until then, and when cancelled, the current one.
            databaseShown = databaseIndex;
            host.Post(() => RefreshOne(nameof(DatabaseIndex)));
            var current = worktree;
            host.Start(current.Name, () => host.SwitchDatabaseAsync(current, mode));
        }
    }

    public bool CanSwitchDatabase => Setup is { IsBusy: false };
    public string? DatabaseTooltip => Setup is null ? Strings.Worktrees_SetUpFirst : null;

    // Setup: queued, running (the step), set up or failed (why), with its log and Run Setup Again.
    public bool IsNotSetUp => Setup is null;
    public bool IsSetUp => Setup is not null;
    public bool IsBusy => Setup is { IsBusy: true };
    public string BusyText => Setup switch
    {
        { State: Status.WorktreeState.Queued } => Strings.Worktrees_Waiting,
        { Step.Length: > 0 } setup => setup.Step + "…",
        _ => Strings.Worktrees_SettingUp,
    };
    public bool IsFailed => Setup is { IsBusy: false, IsFailed: true };
    public string FailedText => Setup is { Error.Length: > 0 } setup ? setup.Error : Strings.Worktrees_SetupFailedSentence;
    public bool IsReady => Setup is { IsBusy: false, IsFailed: false };
    public bool HasLog => Setup is { Log.Length: > 0 };
    public bool CanRunAgain => Setup is { IsBusy: false };

    public OutputSource? Output => worktree.IsNode ? DevServer.Output : null;

    public void Hide()
    {
        if (IsShown)
        {
            IsShown = false;
            OnPropertyChanged(nameof(IsShown));
        }
    }

    public void Update(Status.Site value, Status? status)
    {
        worktree = value;
        hasParent = status?.Sites.Any(s => s.Name == value.Parent) == true;
        IsShown = true;
        // A mode left to flock.yml is unknown here, unless the worktree uses the site's database.
        databaseIndex = Array.IndexOf(DatabaseModes, value.Worktree?.DbMode ?? "");
        if (databaseIndex < 0 && value.Worktree is not { HasOwnDatabase: true })
        {
            databaseIndex = 0;
        }
        databaseShown = databaseIndex;
        DevServer.Update(value);
        Refresh();
    }

    [RelayCommand]
    private void Open() => host.Open(worktree);

    [RelayCommand]
    private void Reveal() => host.Reveal(worktree.Path);

    [RelayCommand]
    private void Remove()
    {
        var current = worktree;
        host.Start(current.Name, () => host.RemoveWorktreeAsync(current));
    }

    [RelayCommand]
    private void OpenSite() => host.Select(Parent);

    [RelayCommand]
    private void SetUp()
    {
        var current = worktree;
        host.Start(current.Name, () => host.SetUpWorktreeAsync(current, false));
    }

    [RelayCommand]
    private void RunSetupAgain()
    {
        var current = worktree;
        host.Start(current.Name, () => host.SetUpWorktreeAsync(current, true));
    }

    [RelayCommand]
    private void ShowLog()
    {
        var current = worktree;
        host.Start("log:" + current.Name, () => host.ShowSetupLogAsync(current));
    }
}

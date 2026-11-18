using Flock.Kit;
using Flock.Tray.Features.Sites;
using Flock.Tray.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
namespace Flock.Tray.Settings;

/// <summary>
///     Asks for a new worktree's branch (empty: Flock makes a name up), where it starts (the site's
///     branches, once loaded) and its database.
/// </summary>
internal sealed partial class NewWorktreeView : UserControl
{
    private readonly Status.Site site;
    // values[i] is where item i of FromBox starts ("" the current commit, null the separator).
    private readonly List<string?> values = [];
    private SiteBranches branches = new();
    private bool loaded;

    public NewWorktreeView(Status.Site site)
    {
        this.site = site;
        InitializeComponent();
        DatabaseBox.Items.Add(Strings.Worktrees_ProjectDefault);
        foreach (var item in WorktreeDetailViewModel.DatabaseChoices)
        {
            DatabaseBox.Items.Add(item);
        }
        DatabaseBox.SelectedIndex = 0;
        FillFrom();
        Update();
    }

    public string Branch => BranchBox.Text.Trim();

    /// <summary>Where a new branch starts; null for the current commit, or a branch that exists (it is checked out as it is).</summary>
    public string? Start => !Exists && FromBox.SelectedIndex >= 0 && values[FromBox.SelectedIndex] is { Length: > 0 } start ? start : null;

    /// <summary>The database mode, or null for the project's.</summary>
    public string? Database => DatabaseBox.SelectedIndex > 0 ? WorktreeDetailViewModel.DatabaseModes[DatabaseBox.SelectedIndex - 1] : null;

    private bool Exists => branches.Local.Contains(Branch);

    public async Task LoadBranchesAsync(SitesService sites, CancellationToken token)
    {
        // An older daemon can't list them: only the current commit is offered.
        if (await sites.SiteBranchesAsync(site.Name, token) is { } listed)
        {
            branches = listed;
            FillFrom();
        }
        loaded = true;
        Update();
    }

    // The current commit when detached; the local branches (current, default, the rest); the remote ones.
    private void FillFrom()
    {
        FromBox.Items.Clear();
        values.Clear();
        if (branches.Current.Length == 0)
        {
            FromBox.Items.Add(Strings.Worktrees_New_CurrentCommit);
            values.Add("");
        }
        foreach (var name in branches.Ordered())
        {
            FromBox.Items.Add(name == branches.Current ? Localization.Format(Strings.Worktrees_New_CurrentBranch, name)
                : name == branches.Default ? Localization.Format(Strings.Worktrees_New_DefaultBranch, name) : name);
            values.Add(name);
        }
        if (branches.Remote.Count > 0)
        {
            FromBox.Items.Add(new ComboBoxItem
            {
                IsEnabled = false,
                Content = new Border { Height = 1, Background = Ui.Brush("DividerStrokeColorDefaultBrush") },
                HorizontalContentAlignment = HorizontalAlignment.Stretch,
            });
            values.Add(null);
            foreach (var name in branches.Remote)
            {
                FromBox.Items.Add(name);
                values.Add(name);
            }
        }
        FromBox.SelectedIndex = Math.Max(0, values.IndexOf(branches.Current));
    }

    private void BranchChanged(object sender, TextChangedEventArgs args) => Update();

    private void Update()
    {
        FromBox.IsEnabled = loaded && !Exists;
        var typed = Branch;
        var address = $"{SiteBranches.Slug(typed)}.{site.Domain}";
        ExplanationText.Text = typed.Length == 0 ? Localization.Format(Strings.Worktrees_New_ExplainUnnamed, site.Name)
            : Exists ? Localization.Format(Strings.Worktrees_New_ExplainExisting, typed, site.Name, address)
            : Localization.Format(Strings.Worktrees_New_ExplainNew, site.Name, typed, address);
    }
}

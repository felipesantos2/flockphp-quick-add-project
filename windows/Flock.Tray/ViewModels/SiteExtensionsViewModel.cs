using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Flock.Kit;
using Flock.Tray.Features.Sites;

namespace Flock.Tray.ViewModels;

/// <summary>
///     A site's PHP extensions: what composer.json and the locked packages require, what a Laravel
///     app's configuration shows it uses without requiring it, and whether Flock's PHP has each.
///     Adding one to composer.json is confirmed inline (only one dialog can be open at a time).
/// </summary>
internal sealed partial class SiteExtensionsViewModel(SitesService sites, Status.Site site, Action<Func<Task>> start, CancellationToken token)
    : ObservableObject
{
    private ExtensionReport? report;
    private string? error;
    private string? confirming;
    private string? adding;

    public string Error => error ?? "";
    public bool HasError => error is not null;
    public bool IsLoading => report is null && error is null;
    public bool HasReport => report is not null;
    public string Summary => report is null ? "" : report.Laravel ? Localization.Format(Strings.Sites_ExtensionsLaravel, report.Php) : $"PHP {report.Php}";
    public bool HasNoExtensions => report is { Extensions.Count: 0 };
    public IReadOnlyList<ExtensionItemViewModel> Extensions { get; private set; } = [];
    public string SitePath => site.Path;

    /// <summary>Loads the report; false when it was retired (another site's was asked for meanwhile).</summary>
    public async Task<bool> LoadAsync()
    {
        (report, error) = await sites.SiteExtensionsAsync(site.Name, token);
        if (report is null && error is null)
        {
            return false;
        }
        Render();
        return true;
    }

    internal void Confirm(string name)
    {
        confirming = name;
        Render();
    }

    internal void Cancel()
    {
        confirming = null;
        Render();
    }

    internal void Require(string name) => start(() => AddAsync(name));

    private async Task AddAsync(string name)
    {
        confirming = null;
        adding = name;
        Render();
        (var fresh, error) = await sites.RequireExtensionAsync(site.Name, name, token);
        report = fresh ?? report;
        adding = null;
        Render();
    }

    private void Render()
    {
        Extensions = report?.Extensions.Select(extension => new ExtensionItemViewModel(this, report.Php, extension,
            adding == extension.Name, confirming == extension.Name, adding is null)).ToList() ?? [];
        OnPropertyChanged(string.Empty);
    }
}

/// <summary>An extension in the list: whether Flock's PHP has it, what requires it, and Add to composer.json.</summary>
internal sealed partial class ExtensionItemViewModel(SiteExtensionsViewModel list, string php, PhpExtension extension, bool adding, bool confirming, bool canAdd)
{
    public string Name => extension.Name;
    public string Glyph => !extension.Available ? Ui.GlyphWarning : extension.Declared ? Ui.GlyphPuzzle : Ui.GlyphInfo;
    public string Tone => (!extension.Available ? Ui.Tone.Critical : extension.Declared ? Ui.Tone.Success : Ui.Tone.Caution).ToString();
    public string Detail => (extension.Declared ? Localization.Format(Strings.Sites_ExtensionRequiredBy, string.Join(", ", extension.RequiredBy)) : Localization.Format(Strings.Sites_ExtensionUsed, extension.Hint))
                            + (extension.Available ? "" : " — " + Localization.Format(Strings.Sites_ExtensionMissing, php));
    public bool IsAdding => adding;
    public bool IsConfirming => !adding && confirming;
    public bool CanBeAdded => !adding && !confirming && !extension.Declared;
    public bool CanAdd => canAdd;
    public string RequireTooltip => Localization.Format(Strings.Sites_RequireTooltip, extension.Name, list.SitePath);

    [RelayCommand]
    private void Confirm() => list.Confirm(extension.Name);

    [RelayCommand]
    private void Cancel() => list.Cancel();

    [RelayCommand]
    private void Require() => list.Require(extension.Name);
}

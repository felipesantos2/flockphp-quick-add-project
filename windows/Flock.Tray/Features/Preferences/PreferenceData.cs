using Flock.Kit;

namespace Flock.Tray.Features.Preferences;

/// <summary>Persisted tray preferences. This type has no file or UI side effects.</summary>
public sealed record PreferenceData
{
    public string Editor { get; init; } = "PhpStorm";
    public bool DumpsLatestOnly { get; init; } = true;
    public string InstalledFlock { get; init; } = "";
    public string Channel { get; init; } = "stable";
    /// <summary>The sites whose worktrees the panel shows, one per line (a string, so the record keeps its value equality).</summary>
    public string ExpandedSites { get; init; } = "";
    /// <summary>The UI language: "system" (Windows' preferred languages) or one of Localization.Languages. Applied at startup.</summary>
    public string Language { get; init; } = Localization.System;
}
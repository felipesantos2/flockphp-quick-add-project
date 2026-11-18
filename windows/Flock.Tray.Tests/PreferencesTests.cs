using Flock.Tray.Features.Preferences;

namespace Flock.Tray.Tests;

public sealed class PreferencesTests
{
    [Fact]
    public async Task LoadIsPassiveAndIgnoresLegacyEditorChoiceInAnyOrder()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var path = Path.Combine(directory.FullName, "tray.json");
            const string original = "{\"DumpsLatestOnly\":false,\"EditorChoice\":\"VisualStudioCode\",\"Channel\":\"beta\",\"Editor\":\"PhpStorm\"}";
            await File.WriteAllTextAsync(path, original);
            var service = new PreferencesService(path);

            var value = await service.LoadAsync();

            Assert.Equal("PhpStorm", value.Editor);
            Assert.False(value.DumpsLatestOnly);
            Assert.Equal("beta", value.Channel);
            Assert.Equal(original, await File.ReadAllTextAsync(path));
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task MissingAndCorruptFilesUseDefaultsWithoutWriting()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var path = Path.Combine(directory.FullName, "tray.json");
            var missing = new PreferencesService(path);
            Assert.Equal(new PreferenceData(), await missing.LoadAsync());
            Assert.False(File.Exists(path));

            await File.WriteAllTextAsync(path, "{not json");
            var log = new List<Exception>();
            var corrupt = new PreferencesService(path, log: log.Add);
            Assert.Equal(new PreferenceData(), await corrupt.LoadAsync());
            Assert.NotEmpty(log);
            Assert.Equal("{not json", await File.ReadAllTextAsync(path));
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task FirstSaveCreatesMissingParentDirectoriesAndPersistsPreferences()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var parent = Path.Combine(directory.FullName, "dev", "new-build");
            var path = Path.Combine(parent, "tray.json");
            var service = new PreferencesService(path);
            Assert.Equal(new PreferenceData(), await service.LoadAsync());
            Assert.False(Directory.Exists(parent));
            PreferenceData? published = null;
            service.StateChanged += () => published = service.Snapshot;

            var saved = await service.UpdateAsync(value => value with { Channel = "beta" });

            Assert.Equal("beta", saved.Channel);
            Assert.Equal(saved, published);
            Assert.Equal(saved, await new PreferencesService(path).LoadAsync());
            Assert.Null(service.ActionError);
            Assert.Empty(Directory.GetFiles(parent, "*.tmp.*"));
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task FailedSaveAndDismissalPublishTheCompleteState()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var service = new PreferencesService(directory.FullName);
            var notifications = 0;
            service.StateChanged += () => notifications++;

            await Assert.ThrowsAnyAsync<UnauthorizedAccessException>(() => service.UpdateAsync(value => value with { Channel = "beta" }));
            Assert.Equal(1, notifications);
            Assert.NotNull(service.Error);

            service.DismissError();

            Assert.Equal(2, notifications);
            Assert.Null(service.Error);
            Assert.Equal(new PreferenceData(), service.Snapshot);
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task NoOpUpdateClearsAnErrorAndPublishesState()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var service = new PreferencesService(directory.FullName);
            var notifications = 0;
            service.StateChanged += () => notifications++;

            await Assert.ThrowsAnyAsync<UnauthorizedAccessException>(() => service.UpdateAsync(value => value with { Channel = "beta" }));
            await service.UpdateAsync(value => value);

            Assert.Equal(2, notifications);
            Assert.Null(service.Error);
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task ConcurrentUpdatesMergeInsideTheSaveGate()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var service = new PreferencesService(Path.Combine(directory.FullName, "tray.json"));
            await Task.WhenAll(
                service.UpdateAsync(value => value with { Editor = "Cursor" }),
                service.UpdateAsync(value => value with { Channel = "beta" }));

            var value = await service.LoadAsync();
            Assert.Equal("Cursor", value.Editor);
            Assert.Equal("beta", value.Channel);
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task FailedCommitKeepsThePreviousFileAndExposesTheError()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var service = new PreferencesService(directory.FullName);
            await Assert.ThrowsAnyAsync<UnauthorizedAccessException>(() => service.UpdateAsync(value => value with { Channel = "beta" }));
            Assert.NotNull(service.ActionError);
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task CanceledUpdateDoesNotWriteOrPublish()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var path = Path.Combine(directory.FullName, "tray.json");
            var service = new PreferencesService(path);
            using var canceled = new CancellationTokenSource();
            canceled.Cancel();

            await Assert.ThrowsAnyAsync<OperationCanceledException>(() => service.UpdateAsync(
                value => value with { Channel = "beta" }, canceled.Token));

            Assert.Equal(new PreferenceData(), service.Snapshot);
            Assert.False(File.Exists(path));
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }

    [Fact]
    public async Task LanguageFollowsTheSystemUntilChosen()
    {
        var directory = Directory.CreateTempSubdirectory("flock-preferences-");
        try
        {
            var path = Path.Combine(directory.FullName, "tray.json");
            // A file written before the Language preference existed.
            await File.WriteAllTextAsync(path, "{\"Channel\":\"beta\"}");
            Assert.Equal("system", (await new PreferencesService(path).LoadAsync()).Language);

            await new PreferencesService(path).UpdateAsync(value => value with { Language = "cs" });

            var saved = await new PreferencesService(path).LoadAsync();
            Assert.Equal(("cs", "beta"), (saved.Language, saved.Channel));
        }
        finally
        {
            Directory.Delete(directory.FullName, recursive: true);
        }
    }
}

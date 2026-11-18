using System.Runtime.ExceptionServices;
using System.Text.Json;
using System.Text.Json.Serialization;
using Flock.Tray.Infrastructure;
using Microsoft.Extensions.Logging;

namespace Flock.Tray.Features.Preferences;

/// <summary>Loads preferences without writing and commits updates atomically.</summary>
public sealed class PreferencesService
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = null,
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
    };

    private readonly string path;
    private readonly IUiDispatcher? dispatcher;
    private readonly ILogger? logger;
    private readonly Action<Exception>? log;
    private readonly SemaphoreSlim gate = new(1, 1);
    private PreferenceData snapshot = new();
    private bool loaded;

    public PreferencesService(string? path = null, IUiDispatcher? dispatcher = null, Action<Exception>? log = null)
    {
        this.path = path ?? DefaultPath;
        this.dispatcher = dispatcher;
        this.log = log;
    }

    public PreferencesService(ILogger<PreferencesService> logger, IUiDispatcher? dispatcher = null)
        : this((string?)null, dispatcher, null)
    {
        this.logger = logger;
    }

    public static string DefaultPath => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Flock", "tray.json");

    public PreferenceData Snapshot
    {
        get
        {
            lock (gate)
            {
                return snapshot;
            }
        }
    }

    public string? Error { get; private set; }
    public string? ActionError => Error;

    public event Action? StateChanged;

    /// <summary>Reads the existing file once. A missing file uses defaults without creating it.</summary>
    public async Task<PreferenceData> LoadAsync(CancellationToken cancellationToken = default)
    {
        await gate.WaitAsync(cancellationToken);
        try
        {
            if (!loaded)
            {
                snapshot = await ReadAsync(cancellationToken);
                loaded = true;
            }
            return snapshot;
        }
        finally
        {
            gate.Release();
        }
    }

    public void DismissError()
    {
        if (Error is not null)
        {
            Error = null;
            PublishStateChanged();
        }
    }

    /// <summary>Applies one update to the latest committed value and publishes only after commit.</summary>
    public async Task<PreferenceData> UpdateAsync(Func<PreferenceData, PreferenceData> update, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(update);
        await gate.WaitAsync(cancellationToken);
        string? temporary = null;
        var committed = false;
        var notify = false;
        ExceptionDispatchInfo? failure = null;
        try
        {
            if (!loaded)
            {
                snapshot = await ReadAsync(cancellationToken);
                loaded = true;
            }

            var next = update(snapshot);
            ArgumentNullException.ThrowIfNull(next);
            if (next == snapshot)
            {
                if (Error is not null)
                {
                    Error = null;
                    notify = true;
                }
            }
            else
            {
                temporary = path + ".tmp." + Guid.NewGuid().ToString("N");
                await WriteTemporaryAsync(temporary, next, cancellationToken);
                cancellationToken.ThrowIfCancellationRequested();
                File.Move(temporary, path, overwrite: true);
                temporary = null;
                committed = true;
                snapshot = next;
                Error = null;
                notify = true;
            }
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException or OperationCanceledException)
        {
            Error = committed ? null : SafeMessage(error);
            notify = true;
            if (!committed)
            {
                Log(error, "save");
                failure = ExceptionDispatchInfo.Capture(error);
            }
        }
        finally
        {
            if (temporary is not null)
            {
                TryDelete(temporary);
            }
            gate.Release();
        }

        if (notify)
        {
            await PublishStateChangedAsync();
        }
        failure?.Throw();
        return snapshot;
    }

    private void PublishStateChanged()
    {
        if (dispatcher is not null)
        {
            _ = PublishStateChangedAsync();
            return;
        }
        StateChanged?.Invoke();
    }

    private Task PublishStateChangedAsync()
    {
        if (dispatcher is not null)
        {
            return dispatcher.InvokeAsync(() => StateChanged?.Invoke(), CancellationToken.None);
        }
        StateChanged?.Invoke();
        return Task.CompletedTask;
    }

    private async Task<PreferenceData> ReadAsync(CancellationToken cancellationToken)
    {
        if (!File.Exists(path))
        {
            return new PreferenceData();
        }

        try
        {
            await using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read,
                bufferSize: 4096, options: FileOptions.Asynchronous | FileOptions.SequentialScan);
            return await JsonSerializer.DeserializeAsync<PreferenceData>(stream, JsonOptions, cancellationToken)
                ?? new PreferenceData();
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException)
        {
            Log(error, "read");
            return new PreferenceData();
        }
    }

    private static Task WriteTemporaryAsync(string file, PreferenceData value, CancellationToken cancellationToken) => Task.Run(async () =>
    {
        var data = JsonSerializer.Serialize(value, JsonOptions);
        Directory.CreateDirectory(Path.GetDirectoryName(file)!);
        await using var stream = new FileStream(file, FileMode.CreateNew, FileAccess.Write, FileShare.None,
            bufferSize: 4096, options: FileOptions.Asynchronous | FileOptions.SequentialScan);
        await using var writer = new StreamWriter(stream);
        await writer.WriteAsync(data.AsMemory(), cancellationToken);
        await writer.FlushAsync(cancellationToken);
        stream.Flush(flushToDisk: true);
    }, CancellationToken.None);

    private void Log(Exception error, string operation)
    {
        logger?.LogError(error, "Could not {Operation} Flock preferences at {Path}", operation, path);
        log?.Invoke(error);
    }

    private static string SafeMessage(Exception error) => error is OperationCanceledException ? Strings.Settings_SaveCanceled : error.Message;

    private static void TryDelete(string file)
    {
        try
        {
            File.Delete(file);
        }
        catch (IOException)
        {
        }
        catch (UnauthorizedAccessException)
        {
        }
    }
}

using System.Diagnostics;
using Microsoft.Extensions.Logging;

namespace Flock.Tray.Infrastructure;

public readonly record struct ShellLaunchResult(bool Succeeded, string Message)
{
    public static ShellLaunchResult Success() => new(true, "");
}

/// <summary>Owns default-association, Explorer, and connection launches and their visible outcomes.</summary>
public interface IShellLauncher
{
    ShellLaunchResult Open(string target);
    ShellLaunchResult Reveal(string path);
    ShellLaunchResult OpenConnection(string executable, string connection);
}

public sealed class ShellLauncher : IShellLauncher
{
    private readonly ILogger<ShellLauncher> logger;
    private readonly Func<ProcessStartInfo, Process?> startProcess;

    public ShellLauncher(ILogger<ShellLauncher> logger, Func<ProcessStartInfo, Process?>? startProcess = null)
    {
        this.logger = logger;
        this.startProcess = startProcess ?? Process.Start;
    }

    public ShellLaunchResult Open(string target) => Start("open", new ProcessStartInfo(target) { UseShellExecute = true }, Strings.App_CouldNotOpen);

    public ShellLaunchResult Reveal(string path)
    {
        var arguments = Directory.Exists(path) && !File.Exists(path) ? $"\"{path}\"" : $"/select,\"{path}\"";
        return Start("reveal", new ProcessStartInfo("explorer.exe", arguments) { UseShellExecute = true }, Strings.App_CouldNotReveal);
    }

    public ShellLaunchResult OpenConnection(string executable, string connection)
    {
        var start = new ProcessStartInfo(executable) { UseShellExecute = false };
        start.ArgumentList.Add(connection);
        return Start("connection", start, Strings.App_CouldNotOpenConnection);
    }

    private ShellLaunchResult Start(string operation, ProcessStartInfo start, string message)
    {
        try
        {
            using var process = startProcess(start);
            return process is null ? Failure(operation, message, null) : ShellLaunchResult.Success();
        }
        catch (Exception error)
        {
            return Failure(operation, message, error);
        }
    }

    private ShellLaunchResult Failure(string operation, string message, Exception? error)
    {
        if (error is null)
        {
            logger.LogError("Shell operation {Operation} returned no process.", operation);
        }
        else
        {
            logger.LogError(error, "Shell operation {Operation} failed.", operation);
        }
        return new ShellLaunchResult(false, message);
    }
}
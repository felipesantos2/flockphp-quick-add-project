using System.Globalization;
using System.Runtime.CompilerServices;

namespace Flock.Tray.Tests;

internal static class TestCulture
{
    /// <summary>The tests expect English text and en-US formats, whatever the machine's language and region.</summary>
#pragma warning disable CA2255 // A test assembly sets its culture before any test runs.
    [ModuleInitializer]
#pragma warning restore CA2255
    internal static void UseEnglish()
    {
        var english = CultureInfo.GetCultureInfo("en-US");
        CultureInfo.DefaultThreadCurrentCulture = CultureInfo.DefaultThreadCurrentUICulture = english;
        CultureInfo.CurrentCulture = CultureInfo.CurrentUICulture = english;
    }
}

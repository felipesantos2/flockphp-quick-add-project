import Foundation
import Testing

/// Resources/Localizable.xcstrings, which tools/i18n generates from lang/ and build-app.sh compiles
/// into the app: every text is in each of the app's languages (English where a language lacks it),
/// with the placeholders its key has.
private let catalogURL = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    .appendingPathComponent("Resources/Localizable.xcstrings")

private struct Catalog: Decodable {
    var sourceLanguage: String
    var strings: [String: Entry]

    struct Entry: Decodable {
        var localizations: [String: Localization]?
    }

    struct Localization: Decodable {
        var stringUnit: StringUnit?
        var variations: Variations?
    }

    struct Variations: Decodable {
        var plural: [String: Localization]?
    }

    struct StringUnit: Decodable {
        var state: String
        var value: String
    }
}

/// "%@", "%lld", ... in order of their argument, so "%2$@ %1$lld" reads as ["lld", "@"].
private func placeholders(_ text: String) -> [String] {
    let regex = /%(?:(\d+)\$)?(lld|ld|d|@)/
    var next = 0
    var found: [(Int, String)] = []
    for match in text.matches(of: regex) {
        next += 1
        found.append((match.1.flatMap { Int($0) } ?? next, String(match.2)))
    }
    return found.sorted { $0.0 < $1.0 }.map(\.1)
}

@Test func everyTextIsInEveryLanguage() throws {
    let catalog = try JSONDecoder().decode(Catalog.self, from: Data(contentsOf: catalogURL))
    #expect(catalog.sourceLanguage == "en")
    #expect(!catalog.strings.isEmpty)
    let languages = Set(catalog.strings.values.flatMap { $0.localizations?.keys.map { $0 } ?? [] })
    #expect(languages.contains("en"))

    for (key, entry) in catalog.strings {
        // The key is the text's key and its arguments: "php.update_to %@".
        let arguments = placeholders(key)
        for language in languages {
            guard let localization = entry.localizations?[language] else {
                Issue.record("\(language) is missing for \(key.debugDescription)")
                continue
            }
            if let plural = localization.variations?.plural {
                #expect(Set(plural.keys).isSuperset(of: ["other"]), "\(language) plural forms of \(key.debugDescription)")
                var withoutCount = arguments
                if let count = withoutCount.firstIndex(of: "lld") {
                    withoutCount.remove(at: count)
                }
                for (form, variant) in plural {
                    #expect(variant.stringUnit?.state == "translated", "\(language) \(form) of \(key.debugDescription)")
                    let found = placeholders(variant.stringUnit?.value ?? "")
                    // "one" may leave the count out: "Show its worktree".
                    #expect(found == arguments || form == "one" && found == withoutCount,
                            "\(language) \(form) placeholders of \(key.debugDescription)")
                }
            } else {
                #expect(localization.stringUnit?.state == "translated", "\(language) of \(key.debugDescription)")
                if let value = localization.stringUnit?.value {
                    #expect(placeholders(value) == arguments, "\(language) placeholders of \(key.debugDescription): \(value.debugDescription)")
                }
            }
        }
    }
}

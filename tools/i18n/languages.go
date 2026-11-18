package main

// What the generator knows about a language: lang/<code>/ only needs translating. A language
// that isn't listed here can't be built into the apps yet: add it with its CLDR plural rules.
type Language struct {
	Code   string // the folder in lang/: a BCP 47 tag, as Apple, Windows and gettext name it
	Name   string // in English, for the generated files' comments
	Native string // in the language itself, for the language pickers
	// The CLDR plural categories of whole numbers, in the order of PluralForms' indices.
	Forms []string
	// Categories macOS also asks for, for fractions; it gets the "other" form.
	Extra []string
	// gettext's Plural-Forms: Forms' index for a count n. Also C#, see windows.go.
	PluralForms string
	// The installer's messages file, from Inno Setup's compiler:Languages\; "" when Inno Setup
	// has none, and setup speaks English.
	Inno string
}

var (
	oneOther  = []string{"one", "other"}
	notOne    = "nplurals=2; plural=(n != 1);"
	slavicCs  = "nplurals=3; plural=(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2;"
	slavicRu  = "nplurals=3; plural=(n%10==1 && n%100!=11) ? 0 : (n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20)) ? 1 : 2;"
	slavicPl  = "nplurals=3; plural=(n==1) ? 0 : (n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20)) ? 1 : 2;"
	zeroOrOne = "nplurals=2; plural=(n > 1);"
	noPlural  = "nplurals=1; plural=0;"
)

// English is the source; the others are what lang/ may hold.
var english = Language{Code: "en", Name: "English", Native: "English", Forms: oneOther, PluralForms: notOne, Inno: "Default.isl"}

var knownLanguages = []Language{
	{Code: "cs", Name: "Czech", Native: "Čeština", Forms: []string{"one", "few", "other"}, Extra: []string{"many"}, PluralForms: slavicCs, Inno: "Czech.isl"},
	{Code: "da", Name: "Danish", Native: "Dansk", Forms: oneOther, PluralForms: notOne, Inno: "Danish.isl"},
	{Code: "de", Name: "German", Native: "Deutsch", Forms: oneOther, PluralForms: notOne, Inno: "German.isl"},
	{Code: "es", Name: "Spanish", Native: "Español", Forms: oneOther, PluralForms: notOne, Inno: "Spanish.isl"},
	{Code: "fi", Name: "Finnish", Native: "Suomi", Forms: oneOther, PluralForms: notOne, Inno: "Finnish.isl"},
	{Code: "fr", Name: "French", Native: "Français", Forms: oneOther, PluralForms: zeroOrOne, Inno: "French.isl"},
	{Code: "hu", Name: "Hungarian", Native: "Magyar", Forms: oneOther, PluralForms: notOne, Inno: "Hungarian.isl"},
	{Code: "it", Name: "Italian", Native: "Italiano", Forms: oneOther, PluralForms: notOne, Inno: "Italian.isl"},
	{Code: "ja", Name: "Japanese", Native: "日本語", Forms: []string{"other"}, PluralForms: noPlural, Inno: "Japanese.isl"},
	{Code: "ko", Name: "Korean", Native: "한국어", Forms: []string{"other"}, PluralForms: noPlural, Inno: "Korean.isl"},
	{Code: "nb", Name: "Norwegian Bokmål", Native: "Norsk bokmål", Forms: oneOther, PluralForms: notOne, Inno: "Norwegian.isl"},
	{Code: "nl", Name: "Dutch", Native: "Nederlands", Forms: oneOther, PluralForms: notOne, Inno: "Dutch.isl"},
	{Code: "pl", Name: "Polish", Native: "Polski", Forms: []string{"one", "few", "many"}, Extra: []string{"other"}, PluralForms: slavicPl, Inno: "Polish.isl"},
	{Code: "pt", Name: "Portuguese", Native: "Português", Forms: oneOther, PluralForms: notOne, Inno: "Portuguese.isl"},
	{Code: "pt-BR", Name: "Brazilian Portuguese", Native: "Português (Brasil)", Forms: oneOther, PluralForms: zeroOrOne, Inno: "BrazilianPortuguese.isl"},
	{Code: "ro", Name: "Romanian", Native: "Română", Forms: []string{"one", "few", "other"},
		PluralForms: "nplurals=3; plural=(n==1) ? 0 : (n==0 || (n%100>0 && n%100<20)) ? 1 : 2;"},
	{Code: "ru", Name: "Russian", Native: "Русский", Forms: []string{"one", "few", "many"}, Extra: []string{"other"}, PluralForms: slavicRu, Inno: "Russian.isl"},
	{Code: "sk", Name: "Slovak", Native: "Slovenčina", Forms: []string{"one", "few", "other"}, Extra: []string{"many"}, PluralForms: slavicCs, Inno: "Slovak.isl"},
	{Code: "sv", Name: "Swedish", Native: "Svenska", Forms: oneOther, PluralForms: notOne, Inno: "Swedish.isl"},
	{Code: "tr", Name: "Turkish", Native: "Türkçe", Forms: oneOther, PluralForms: notOne, Inno: "Turkish.isl"},
	{Code: "uk", Name: "Ukrainian", Native: "Українська", Forms: []string{"one", "few", "many"}, Extra: []string{"other"}, PluralForms: slavicRu, Inno: "Ukrainian.isl"},
	{Code: "zh-Hans", Name: "Simplified Chinese", Native: "简体中文", Forms: []string{"other"}, PluralForms: noPlural},
	{Code: "zh-Hant", Name: "Traditional Chinese", Native: "繁體中文", Forms: []string{"other"}, PluralForms: noPlural},
}

func knownLanguage(code string) (Language, bool) {
	for _, language := range knownLanguages {
		if language.Code == code {
			return language, true
		}
	}
	return Language{}, false
}

// The share of a language's texts that must be translated before the apps offer it. Below it,
// the language stays in lang/ until more of it is translated.
const shipThreshold = 0.7

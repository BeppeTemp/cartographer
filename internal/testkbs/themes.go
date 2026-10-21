package testkbs

import "strings"

// A theme is the invented subject of one profiled KB (D1032): the profile fixes
// the numbers, the theme the words. Every word the profiled KBs contain comes
// from the tables of this file (TestProfiledKBsUseOnlyTheirOwnVocabulary), so
// no real name can get in by accident; nothing here is, or is named after,
// anything in a real setup.

// Roles of a Map: how its concepts read.
const (
	roleTopic    = 'T' // a subject explained once
	roleLog      = 'L' // dated entries of a journal
	roleRef      = 'R' // many short definitions
	roleADR      = 'A' // decisions: context, decision, alternatives, consequences
	roleWork     = 'W' // tasks
	roleSource   = 'S' // the source ledger (a journal)
	roleQuestion = 'Q' // contradictions and open questions (a journal)
)

type tmap struct {
	slug, title string
	role        byte
	types       []string
}

type theme struct {
	name        string
	maps        []tmap
	nouns, adjs []string
	// customMap is the Map whose status vocabulary is outside the stock one.
	customMap string
	custom    []string
	// paths and repos are the keys of the placeholders the bodies cite.
	paths, repos []string
}

// tm parses "slug|Title|role|Type,Type".
func tm(s string) tmap {
	f := strings.Split(s, "|")
	return tmap{slug: f[0], title: f[1], role: f[2][0], types: strings.Split(f[3], ",")}
}

func tms(rows ...string) []tmap {
	out := make([]tmap, len(rows))
	for i, r := range rows {
		out[i] = tm(r)
	}
	return out
}

func words(s string) []string { return strings.Fields(s) }

// themes are keyed by the KB name; the maps of a theme line up with the Maps of
// its profile, largest first.
var themes = map[string]*theme{
	"seed-bank": {
		name: "seed-bank",
		maps: tms(
			"accessions|Accessions|L|Accession",
			"varieties|Varieties|T|Variety,Cultivar,Landrace,Wild-relative",
			"protocols|Protocols|T|Protocol,Checklist",
			"growers|Growers|T|Grower,Partner",
			"trials|Trials|T|Trial,Observation",
			"vaults|Storage Vaults|T|Vault,Equipment",
			"glossary|Glossary|R|Term",
			"decisions|Decisions|A|Decision",
			"regions|Regions|T|Region",
			"methods|Methods|T|Method",
			"sources|Sources|S|Source",
			"contacts|Contacts|T|Contact",
			"supplies|Supplies|T|Supply",
		),
		nouns: words("seed pod husk kernel tray sleeve vial jar label crate shelf freezer desiccant sample batch lot germ sprout tuber bean barley millet squash pepper tomato lentil sorghum flax rye oat onion leek radish turnip beet chard kale pea maize clover vetch hemp sesame cress fennel"),
		adjs:  words("dry cold sealed viable dormant heirloom wild hardy early late tall dwarf pale ruddy speckled ancient"),
		paths: []string{"seed-share"}, repos: []string{"seed-tools"},
	},
	"ferry-line": {
		name: "ferry-line",
		maps: tms(
			"voyages|Voyage Log|L|Log",
			"ports|Ports|T|Port,Terminal,Berth,Route,Service",
			"vessels|Vessels|T|Vessel,Equipment,Class,Refit",
			"incidents|Incidents|L|Incident",
			"timetables|Timetables|T|Timetable,Fare",
			"procedures|Procedures|T|Procedure,Checklist,Runbook",
			"decisions|Decisions|A|Decision",
			"sources|Sources|S|Source",
			"glossary|Glossary|R|Term",
		),
		nouns:     words("ferry berth quay gangway hull bridge galley deck mooring ramp tide pilot ticket fare manifest lifeboat anchor rudder funnel bulkhead wharf pier jetty harbour buoy crossing cabin lounge engine propeller ballast hawser bollard fender lantern signal radar chart log cargo vehicle passenger crew shift timetable tender"),
		adjs:      words("morning evening night coastal island northern southern seasonal express stormy calm foggy heavy light reserve scheduled"),
		customMap: "timetables", custom: []string{"scheduled", "sailed", "cancelled"},
		paths: []string{"fleet-share", "harbour-share"}, repos: []string{"fleet-tools", "timetable-scripts"},
	},
	"observatory": {
		name: "observatory",
		maps: tms(
			"sites|Sites|T|Site,Dome,Instrument",
			"nightlogs|Night Logs|L|Night",
			"instruments|Instruments|T|Instrument,Camera,Mount",
			"targets|Targets|T|Target,Catalogue",
			"procedures|Procedures|T|Procedure,Checklist,Policy",
			"software|Software|T|Tool,Script,Pipeline",
			"decisions|Decisions|A|Decision",
			"incidents|Incidents|L|Incident",
			"rotas|Rotas|T|Role,Rota",
			"glossary|Glossary|R|Term",
			"sources|Sources|S|Source",
			"contacts|Contacts|T|Contact",
		),
		nouns: words("dome mount tripod lens mirror sensor filter focuser guider camera shutter telescope eyepiece collimator finder battery cable enclosure roof tracker nebula galaxy cluster comet planet moon star field frame stack dark flat bias plate catalogue transit horizon seeing haze dew heater rail pier tube vane"),
		adjs:  words("clear cloudy steady faint bright dim northern southern remote rooftop portable motorised calibrated stacked wide narrow"),
		paths: []string{"shared-drive"}, repos: []string{"observatory-tools", "stacking-scripts"},
	},
	"library-consortium": {
		name: "library-consortium",
		maps: tms(
			"meetings|Meetings|L|Meeting",
			"decisions|Decisions|A|Decision",
			"schema|Schema|T|Schema,Field,Vocabulary",
			"systems|Systems|T|System,Interface,Component",
			"workflows|Workflows|T|Workflow,Procedure",
			"requirements|Requirements|T|Requirement,Constraint,Risk",
			"glossary|Glossary|R|Term",
			"members|Members|T|Member",
			"integrations|Integrations|T|Integration",
		),
		nouns: words("catalogue record field index thesaurus schema vocabulary loan hold branch shelf volume journal issue title author subject heading authority identifier harvest feed mapping import export queue batch patron card desk request transfer union archive copy edition series imprint binding stack reader portal search facet ranking"),
		adjs:  words("shared local central legacy bilingual public restricted interim canonical merged duplicate linked regional annual nightly manual"),
		paths: []string{"catalogue-share"}, repos: []string{"catalogue-tools"},
	},
	"mountain-rescue": {
		name: "mountain-rescue",
		maps: tms(
			"callouts|Callouts|L|Callout",
			"routes|Routes|T|Route,Hut,Pass,Hazard,Crossing,Trail",
			"equipment|Equipment|T|Equipment,Kit",
			"procedures|Procedures|T|Procedure,Checklist",
			"teams|Teams|T|Team,Role",
			"weather|Weather|T|Forecast,Pattern",
			"training|Training|T|Course,Drill",
			"decisions|Decisions|A|Decision",
			"glossary|Glossary|R|Term",
			"contacts|Contacts|T|Contact",
		),
		nouns:     words("ridge couloir hut pass glacier scree belay rope harness helmet beacon probe shovel litter stretcher winch radio flare bivouac trail summit saddle gully cornice avalanche forecast snowpack crevasse crampon axe tent stove sled drone helicopter landing route crossing weather wind cloud"),
		adjs:      words("steep icy exposed loose windy whiteout summer winter spring night remote marked closed seasonal alpine technical"),
		customMap: "weather", custom: []string{"standby"},
		paths: []string{"rescue-share"}, repos: []string{"rescue-tools"},
	},
	"vineyard-coop": {
		name: "vineyard-coop",
		maps: tms(
			"parcels|Parcels|T|Parcel,Block,Row,Terrace,Plot,Slope",
			"varieties|Varieties|T|Variety,Clone,Rootstock",
			"tasks|Tasks|W|Task,Decision",
			"equipment|Equipment|T|Equipment,Maintenance",
			"cellar|Cellar|T|Tank,Barrel,Batch,Protocol",
			"members|Members|T|Member,Contact",
			"harvest-log|Harvest Log|L|Harvest",
			"sources|Sources|S|Source",
			"glossary|Glossary|R|Term",
			"weather-log|Weather Log|L|Reading",
			"questions|Open Questions|Q|Contradiction",
		),
		nouns: words("vine row block terrace slope trellis grape cluster must barrel tank press cellar bottle cork label vintage harvest bud shoot leaf canopy root graft clone soil cover-crop sprayer tractor crate hopper lees yeast sugar acid cuvee blend tasting frost hail drought"),
		adjs:  words("young old dry ripe green late early sunny shaded steep organic sparkling still red white rose"),
	},
	"choir-season": {
		name: "choir-season",
		maps: tms(
			"rehearsals|Rehearsals|L|Rehearsal",
			"repertoire|Repertoire|T|Piece",
			"members|Members|T|Member",
			"venues|Venues|T|Venue",
			"glossary|Glossary|R|Term",
		),
		nouns:     words("score part voice soprano alto tenor bass chorus verse refrain cadence rest tempo key chord rehearsal warmup concert programme hall chapel stage riser folder pitch conductor accompanist piano organ mass motet madrigal carol anthem canon round hymn encore tour audition ticket poster"),
		adjs:      words("opening closing spring winter festive quiet loud slow brisk sacred folk new revived difficult joint final"),
		customMap: "rehearsals", custom: []string{"rehearsed", "performed", "shelved"},
		paths: []string{"choir-share"}, repos: []string{"choir-tools"},
	},
	"museum-restoration": {
		name: "museum-restoration",
		maps: tms(
			"objects|Objects|T|Object,Fragment,Panel,Frame,Textile",
			"treatment-log|Treatment Log|L|Treatment",
			"materials|Materials|T|Material,Solvent",
			"condition-reports|Condition Reports|L|Report",
		),
		nouns: words("panel canvas frame varnish pigment binder solvent swab scalpel lamp microscope camera gauge crack flake stain patina gilding lining mount crate vitrine label accession loan condition treatment consolidant adhesive tissue mat board humidity mould insect dust fibre thread ceramic bronze ivory"),
		adjs:  words("fragile faded cracked darkened cleaned stable delicate original later repaired framed unframed small large urgent routine"),
		paths: []string{"studio-share"}, repos: []string{"studio-tools"},
	},
	"bike-share-pilot": {
		name: "bike-share-pilot",
		maps: tms(
			"stations|Stations|T|Station,Zone",
			"operations|Operations|T|Procedure,Checklist",
			"ride-log|Ride Log|L|Log",
			"sources|Sources|S|Source",
		),
		nouns: words("station dock bike saddle lock battery wheel pedal chain brake bell light basket rack map zone route trip ride fare pass token app gate cable panel sensor rebalancing van depot repair tyre helmet permit survey counter heatmap"),
		adjs:  words("busy quiet central hilly flat riverside campus downtown electric classic broken charged empty full weekday weekend"),
	},
}

// The vocabulary every theme shares.
var (
	// fillerPeriods, fillerRoles and fillerTemplates build the sentences of
	// every body; {n} {n2} {a} {period} {role} are slots.
	fillerPeriods = words("week month season shift quarter day")
	fillerRoles   = words("keeper warden clerk steward lead coordinator reviewer curator")
	fillerSents   = []string{
		"The {a} {n} is checked against the {n2} before every {period}.",
		"Each {n} is recorded by the {role} and reviewed with the {n2} at the end of the {period}.",
		"A {a} {n} usually follows the {n2}, which is why the {role} keeps both in view.",
		"Nothing is moved until the {n} has been compared with the last {n2} on file.",
		"When the {n} looks {a}, the {role} asks for a second look at the {n2}.",
		"The {n2} is kept next to the {n} so that the {period} handover stays short.",
		"Last {period} the {role} found a {a} {n} and noted it in the {n2} record.",
		"Numbers for the {n} are rounded and the {n2} is quoted as found.",
		"If the {n} is missing, the {role} falls back to the {a} {n2}.",
		"This page stays short on purpose: the {n} matters more than the {n2}.",
		"A {role} can change the {n} without asking, but the {n2} needs a second pair of eyes.",
		"The {a} {n} was agreed after two {period}s of trial use.",
	}
	// fillerHeadings are the section titles of a topical page.
	fillerHeadings = words("Overview Background Scope Details Procedure Findings Measurements Caveats History Checks Notes Examples Rules Responsibilities Timeline Summary")
	// adrHeadings, logHeadings and the like are fixed structures.
	adrHeadings = words("Context Decision Alternatives Consequences")
	logHeadings = []string{"What happened", "Follow-ups"}
	// extraKeys are the frontmatter keys that pad a concept to the profile's key
	// count, in the order they are added.
	extraKeys = words("description area owner origin confidence reviewer scope audience summary cycle revision rank")
	// fillerLevels, fillerCycles and fillerPlaces are the values of those keys.
	fillerLevels = words("high medium low")
	fillerCycles = words("spring summer autumn winter")
	// diacritics are the words that put a non-ASCII letter in about one title
	// in ten: real KBs are not ASCII.
	diacritics = words("café façade fjørd jalapeño naïve crème ångström cañon smörgåsbord señal")
	// assetStems name the assets.
	assetStems = words("sheet table plan sketch export notes draft scan summary")
	// planted words of the defects and the table/code fillers.
	fillerTable = words("item value note")
	fillerCode  = words("check area period")
)

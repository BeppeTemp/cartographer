package core

// The atlas side of a plugin is data only: a read-only view the Atlas draws
// with its own renderers, and an entry that opens an app hosted elsewhere. The
// loaders live in internal/atlasext; this package keeps the client import
// firewall. Nothing here is ever interpreted as markup or code.

// View is the contribution of views/<name>.yaml.
type View struct {
	Title, Description string
	Icon               string // one of the Atlas's icon names
	Scope              string // "kb" or "global"
	Source             ViewSource
	Renderer           string // table, list, stats, keyvalue
	Columns            []ViewColumn
	Limit              int
}

// ViewSource is the public read-only route a view draws.
type ViewSource struct {
	Route string            // a path template; {kb} is the only placeholder
	Query map[string]string // values may hold {kb}
	Items string            // dotted path to the array in the answer, or ""
}

// ViewColumn is one column of a table, or one field of stats and keyvalue.
type ViewColumn struct {
	Key    string // a dotted path in the item
	Label  string
	Format string // text, number, date, relative, badge, concept
}

// App is the contribution of apps/<name>.yaml: a link to an app on its own
// origin, opened in a new tab. The Atlas hands it nothing.
type App struct {
	Title, Description string
	Icon               string
	URL                string
}

package cli

const (
	msgCredKey = "settings file %s names a credential key " +
		"(token/key/auth/secret/password/pat/credential/bearer) — credentials never live in the settings file (export them as env instead)"
	msgTopKey = "settings file %s carries unknown top-level key '%s' (want only %s; " +
		"no server-shaped or org.* namespaces)"
	msgSchemaType   = "settings file %s: $schema must be a string (schema hint only, never fetched)"
	msgOutputObj    = "settings file %s: output must be an object (want {\"format\": \"text\"|\"json\"})"
	msgOutputKey    = "settings file %s: unknown output key '%s' (want only format)"
	msgOutputFormat = "settings file %s: output.format '%s' unknown (want text|json)"
	msgLayoutObj    = "settings file %s: layout must be an object (want {\"compact\": true|false})"
	msgLayoutKey    = "settings file %s: unknown layout key '%s' (want only compact)"
	msgLayoutBool   = "settings file %s: layout.compact must be a boolean (want true|false)"
	msgColorObj     = "settings file %s: color must be an object (want {\"mode\": \"auto\"|\"always\"|\"never\"})"
	msgColorKey     = "settings file %s: unknown color key '%s' (want only mode)"
	msgColorMode    = "settings file %s: color.mode '%s' unknown (want auto|always|never)"
)

const (
	msgPublishObj = "settings file %s: publish must be an object (want {footer, " +
		"attribution, attribution_text, copyright, logo, logo_url, overview, diagrams, fix_prompt})"
	msgPublishKeys = "settings file %s: unknown publish key '%s' (want only footer, " +
		"attribution, attribution_text, copyright, logo, logo_url, overview, diagrams, fix_prompt)"
	msgPublishBool  = "settings file %s: publish.%s must be a boolean (want %s)"
	msgAttrUnknown  = "settings file %s: publish.attribution '%s' unknown (want off|product|full)"
	msgAttrType     = "settings file %s: publish.attribution must be a string (want off|product|full)"
	msgTextType     = "settings file %s: publish.%s must be a string%s"
	msgDiagramsObj  = "settings file %s: publish.diagrams must be an object (want {anchored, structural})"
	msgDiagramsKey  = "settings file %s: unknown publish.diagrams key '%s' (want only anchored, structural)"
	msgDiagramsBool = "settings file %s: publish.diagrams.%s must be a boolean (want true|false)"
)

const (
	msgNotJSON   = "settings file %s is not valid JSON (fix it or delete it)"
	msgNotObject = "settings file %s must hold a JSON object (want only %s)"
)

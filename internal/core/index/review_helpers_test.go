package indexcore

func reviewRows(source string, lang language) []Row {
	s := scanShard("blob", []byte(source), lang, 100)
	f := &treeFile{Path: "uses", Language: string(lang), Imports: s.Imports, Refs: s.Refs}
	return queryCallers(map[string]*packageInfo{".": {dir: ".", files: []*treeFile{f}}}, []target{{pkg: &packageInfo{dir: "."}, name: "save", kind: wordFunc, lang: lang}}).rows
}

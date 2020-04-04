// Copyright 2019 Red Hat, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.)

package v0_2_exp

import (
	"net/url"
	"strings"
	"text/template"

	"github.com/coreos/fcct/translate"

	"github.com/coreos/go-systemd/unit"
	"github.com/coreos/ignition/v2/config/util"
	"github.com/coreos/ignition/v2/config/v3_1_experimental/types"
	"github.com/coreos/vcontext/path"
	"github.com/vincent-petithory/dataurl"
)

var (
	mountUnitTemplate = template.Must(template.New("unit").Parse(`# Generated by FCCT
[Unit]
Before=local-fs.target
Requires=systemd-fsck@{{.Device}}
After=systemd-fsck@{{.Device}}

[Mount]
Where={{.Path}}
What={{.Device}}
Type={{.Format}}

[Install]
RequiredBy=local-fs.target`))
)

// ToIgn3_0 translates the config to an Ignition config. It also returns the set of translations
// it did so paths in the resultant config can be tracked back to their source in the source config.
func (c Config) ToIgn3_1() (types.Config, translate.TranslationSet, error) {
	ret := types.Config{}
	tr := translate.NewTranslator("yaml", "json")
	tr.AddCustomTranslator(translateIgnition)
	tr.AddCustomTranslator(translateFile)
	tr.AddCustomTranslator(translateDirectory)
	tr.AddCustomTranslator(translateLink)
	tr.AddCustomTranslator(translateFilesystem)
	translations := tr.Translate(&c, &ret)
	translations.Merge(c.addMountUnits(&ret))
	return ret, translations, nil
}

func translateIgnition(from Ignition) (to types.Ignition, tm translate.TranslationSet) {
	tr := translate.NewTranslator("yaml", "json")
	to.Version = types.MaxVersion.String()
	tm = tr.Translate(&from.Config, &to.Config).Prefix("config")
	tm.MergeP("proxy", tr.Translate(&from.Proxy, &to.Proxy))
	tm.MergeP("security", tr.Translate(&from.Security, &to.Security))
	tm.MergeP("timeouts", tr.Translate(&from.Timeouts, &to.Timeouts))
	return
}

func translateFile(from File) (to types.File, tm translate.TranslationSet) {
	tr := translate.NewTranslator("yaml", "json")
	tr.AddCustomTranslator(translateFileContents)
	tm = tr.Translate(&from.Group, &to.Group).Prefix("group")
	tm.MergeP("user", tr.Translate(&from.User, &to.User))
	tm.MergeP("append", tr.Translate(&from.Append, &to.Append))
	tm.MergeP("contents", tr.Translate(&from.Contents, &to.Contents))
	to.Overwrite = from.Overwrite
	to.Path = from.Path
	to.Mode = from.Mode
	tm.AddIdentity("overwrite", "path", "mode")
	return
}

func translateFileContents(from FileContents) (to types.FileContents, tm translate.TranslationSet) {
	tr := translate.NewTranslator("yaml", "json")
	tm = tr.Translate(&from.Verification, &to.Verification).Prefix("verification")
	tm.MergeP("httpHeaders", tr.Translate(&from.HTTPHeaders, &to.HTTPHeaders))
	to.Source = from.Source
	to.Compression = from.Compression
	tm.AddIdentity("source", "compression")
	if from.Inline != nil {
		src := (&url.URL{
			Scheme: "data",
			Opaque: "," + dataurl.EscapeString(*from.Inline),
		}).String()
		to.Source = &src
		tm.AddTranslation(path.New("yaml", "inline"), path.New("json", "source"))
	}
	return
}

func translateDirectory(from Directory) (to types.Directory, tm translate.TranslationSet) {
	tr := translate.NewTranslator("yaml", "json")
	tm = tr.Translate(&from.Group, &to.Group).Prefix("group")
	tm.MergeP("user", tr.Translate(&from.User, &to.User))
	to.Overwrite = from.Overwrite
	to.Path = from.Path
	to.Mode = from.Mode
	tm.AddIdentity("overwrite", "path", "mode")
	return
}

func translateLink(from Link) (to types.Link, tm translate.TranslationSet) {
	tr := translate.NewTranslator("yaml", "json")
	tm = tr.Translate(&from.Group, &to.Group).Prefix("group")
	tm.MergeP("user", tr.Translate(&from.User, &to.User))
	to.Target = from.Target
	to.Hard = from.Hard
	to.Overwrite = from.Overwrite
	to.Path = from.Path
	tm.AddIdentity("target", "hard", "overwrite", "path")
	return
}

func translateFilesystem(from Filesystem) (to types.Filesystem, tm translate.TranslationSet) {
	tr := translate.NewTranslator("yaml", "json")
	tm = tr.Translate(&from.MountOptions, &to.MountOptions).Prefix("mount_options")
	tm.MergeP("options", tr.Translate(&from.Options, &to.Options))
	to.Device = from.Device
	to.Label = from.Label
	to.Format = from.Format
	to.Path = from.Path
	to.UUID = from.UUID
	to.WipeFilesystem = from.WipeFilesystem
	tm.AddIdentity("device", "format", "label", "path", "uuid", "wipeFilesystem")
	return
}

func (c Config) addMountUnits(ret *types.Config) translate.TranslationSet {
	ts := translate.NewTranslationSet("yaml", "json")
	if len(c.Storage.Filesystems) == 0 {
		return ts
	}
	unitMap := make(map[string]int, len(ret.Systemd.Units))
	for i, u := range ret.Systemd.Units {
		unitMap[u.Name] = i
	}
	for i, fs := range c.Storage.Filesystems {
		if fs.WithMountUnit == nil || !*fs.WithMountUnit {
			continue
		}
		fromPath := path.New("yaml", "storage", "filesystems", i, "with_mount_unit")
		newUnit := mountUnitFromFS(fs)
		if i, ok := unitMap[unit.UnitNamePathEscape(*fs.Path)+".mount"]; ok {
			// user also specified a unit, only set contents and enabled if the existing unit
			// is unspecified
			u := &ret.Systemd.Units[i]
			unitPath := path.New("json", "systemd", "units", i)
			if u.Contents == nil {
				(*u).Contents = newUnit.Contents
				ts.AddTranslation(fromPath, unitPath.Append("contents"))
			}
			if u.Enabled == nil {
				(*u).Enabled = newUnit.Enabled
				ts.AddTranslation(fromPath, unitPath.Append("enabled"))
			}
		} else {
			unitPath := path.New("json", "systemd", "units", len(ret.Systemd.Units))
			ret.Systemd.Units = append(ret.Systemd.Units, newUnit)
			ts.AddFromCommonSource(fromPath, unitPath, newUnit)
		}
	}
	return ts
}

func mountUnitFromFS(fs Filesystem) types.Unit {
	contents := strings.Builder{}
	err := mountUnitTemplate.Execute(&contents, fs)
	if err != nil {
		panic(err)
	}
	// unchecked deref of path ok, fs would fail validation otherwise
	unitName := unit.UnitNamePathEscape(*fs.Path) + ".mount"
	return types.Unit{
		Name:     unitName,
		Enabled:  util.BoolToPtr(true),
		Contents: util.StrToPtr(contents.String()),
	}
}

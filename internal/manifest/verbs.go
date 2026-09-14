package manifest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/procrastivity/clast/internal/surface"
)

// outputSchemaAnnotation is the cobra.Command.Annotations key a verb's
// declared output schema (if any) is recorded under, mirroring
// internal/surface's own annotation-on-the-command approach: the
// registration lives on the command itself, so this walk needs no parallel
// registry.
const (
	outputSchemaAnnotation = "clast.output-schema"
	aliasOfAnnotation      = "clast.alias-of"
)

// SetOutputSchema records schema as cmd's declared --json output shape. The
// field exists in the manifest shape ready for the first verb that earns
// one; it is not filled speculatively (C3.7).
func SetOutputSchema(cmd *cobra.Command, schema json.RawMessage) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[outputSchemaAnnotation] = string(schema)
}

// SetAliasOf records that this command is an alternate spelling of another
// manifest verb. The command remains a real leaf so flags, usage and schema
// are emitted and checked independently.
func SetAliasOf(cmd *cobra.Command, name string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[aliasOfAnnotation] = name
}

// walkVerbs collects every leaf, non-hidden command under root, regardless
// of surface kind. A hidden command (e.g. an env-gated test fixture) never
// appears in a real install or a manifest walk.
func walkVerbs(root *cobra.Command) ([]Verb, error) {
	var verbs []Verb
	if err := collect(root, root, &verbs); err != nil {
		return nil, err
	}
	return verbs, nil
}

// cobraBuiltins names the commands Cobra adds to every root by default
// (help, completion and its per-shell subcommands) — infrastructure of the
// CLI framework, not a verb, and never annotated with a surface kind.
var cobraBuiltins = map[string]bool{"help": true, "completion": true}

func collect(root, cmd *cobra.Command, out *[]Verb) error {
	for _, child := range cmd.Commands() {
		if child.Hidden || cobraBuiltins[child.Name()] {
			continue
		}
		if len(child.Commands()) > 0 {
			if err := collect(root, child, out); err != nil {
				return err
			}
			continue
		}
		if !child.Runnable() {
			continue
		}

		kind, ok := surface.Of(child)
		if !ok {
			return fmt.Errorf("verb %q declares no manifest surface kind (C3.2) — every registered verb must call surface.Annotate", verbPath(root, child))
		}

		v := Verb{
			Name:        verbPath(root, child),
			Kind:        kind,
			Usage:       usageArgs(child),
			Args:        walkArgs(child),
			Description: child.Short,
		}
		v.AliasOf = child.Annotations[aliasOfAnnotation]
		if schema, ok := child.Annotations[outputSchemaAnnotation]; ok && schema != "" {
			v.OutputSchema = json.RawMessage(schema)
		}
		*out = append(*out, v)
	}
	return nil
}

// verbPath names a command by its full subcommand path, minus the root
// command's own name — "step create", not "clast step create" —
// mirroring internal/cli.Execute's own verbPath helper.
func verbPath(root, cmd *cobra.Command) string {
	path := strings.TrimPrefix(cmd.CommandPath(), root.Name())
	return strings.TrimSpace(path)
}

// usageArgs returns the positional-argument portion of cmd's Use line —
// "<name>" from "new <name>", "" from "version". Cobra's Use line always
// opens with the command's own name; everything after it is the verb's
// declaration of what it takes positionally.
func usageArgs(cmd *cobra.Command) string {
	return strings.TrimSpace(strings.TrimPrefix(cmd.Use, cmd.Name()))
}

// cobraBuiltinFlags names flags Cobra adds to every command automatically
// (--help/-h) — framework infrastructure, not a verb-declared arg.
var cobraBuiltinFlags = map[string]bool{"help": true}

// walkArgs describes cmd's own declared flags (LocalFlags — not the
// inherited global --json/-v pair the chassis binds at root).
func walkArgs(cmd *cobra.Command) []Arg {
	var args []Arg
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if cobraBuiltinFlags[f.Name] {
			return
		}
		_, required := f.Annotations[cobra.BashCompOneRequiredFlag]
		args = append(args, Arg{
			Name:     f.Name,
			Type:     f.Value.Type(),
			Required: required,
		})
	})
	return args
}

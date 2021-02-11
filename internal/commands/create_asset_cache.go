package commands

import (
	"github.com/buildpacks/pack"
	pubcfg "github.com/buildpacks/pack/config"
	"github.com/buildpacks/pack/internal/config"
	"github.com/buildpacks/pack/internal/dist"
	"github.com/buildpacks/pack/internal/image"
	"github.com/buildpacks/pack/logging"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"sort"
)

type CreateAssetCacheFlags struct {
	BuildpackLocator string
	PullPolicy       pubcfg.PullPolicy
	Publish          bool //TODO -Dan- implement
	Registry         string
	Policy           string
}

func CreateAssetCache(logger logging.Logger, cfg config.Config, client PackClient) *cobra.Command {
	var flags CreateAssetCacheFlags

	cmd := &cobra.Command{
		Use:     "create-asset-cache cache-name",
		Hidden:  false,
		Args:    cobra.ExactArgs(1),
		Short:   "Yank build an asset cache using the specified buildpack",
		Example: "pack create-asset-cache /path/to/buildpack/root",
		RunE: logError(logger, func(cmd *cobra.Command, args []string) error {

			// pull policy should indicate preceedence of daemon flags
			if err := validateAssetCacheFlags(&flags); err != nil {
				return err
			}

			stringPolicy := flags.Policy
			pullPolicy, err := pubcfg.ParsePullPolicy(stringPolicy)
			if err != nil {
				panic(err)
				//return errors.Wrapf(err, "parsing pull policy %s", flags.Policy)
			}

			inspectOptions := []pack.InspectBuildpackOptions{}
			switch pullPolicy {
			case pubcfg.PullNever:
				inspectOptions = []pack.InspectBuildpackOptions{{
					BuildpackName: flags.BuildpackLocator,
					Daemon:        true,
					Registry:      flags.Registry,
				}}
			case pubcfg.PullAlways:
				inspectOptions = []pack.InspectBuildpackOptions{{
					BuildpackName: flags.BuildpackLocator,
					Daemon:        false,
					Registry:      flags.Registry,
				}, {
					BuildpackName: flags.BuildpackLocator,
					Daemon:        true,
					Registry:      flags.Registry,
				}}
			case pubcfg.PullIfNotPresent:
				inspectOptions = []pack.InspectBuildpackOptions{{
					BuildpackName: flags.BuildpackLocator,
					Daemon:        true,
					Registry:      flags.Registry,
				}, {
					BuildpackName: flags.BuildpackLocator,
					Daemon:        false,
					Registry:      flags.Registry,
				}}
			}

			buildpackInfo, err := tryInspect(client, inspectOptions)
			switch {
			case err != nil:
				panic(err)
			case buildpackInfo == nil:
				panic("no image found")
			}

			if err := client.CreateAssetCache(cmd.Context(), pack.CreateAssetCacheOptions{
				ImageName: args[0],
				Assets:    getAssets(buildpackInfo),
			}); err != nil {
				panic(err)
			}

			return nil
		}),
	}

	cmd.Flags().StringVarP(&flags.BuildpackLocator, "buildpack", "b", "", "Buildpack Locator")
	cmd.Flags().StringVar(&flags.Policy, "pull-policy", cfg.PullPolicy, "Pull policy to use. Accepted values are always, never, and if-not-present. The default is always")
	cmd.Flags().StringVarP(&flags.Registry, "buildpack-registry", "R", cfg.DefaultRegistryName, "Buildpack Registry by name")
	cmd.Flags().StringVarP(&flags.BuildpackLocator, "config", "c", "", "optional asset-cache.toml to filter assets in the resulting asset cache")

	AddHelpFlag(cmd, "create-asset-cache")
	return cmd
}

func tryInspect(c PackClient, inspectOptions []pack.InspectBuildpackOptions) (*pack.BuildpackInfo, error) {
	var buildpackInfo *pack.BuildpackInfo = nil
	var err error
	for _, inspectOption := range inspectOptions {
		buildpackInfo, err = c.InspectBuildpack(inspectOption)
		switch {
		case errors.Is(err, image.ErrNotFound):
			continue
		case err != nil: // TODO: should this be an error case??
			return nil, err
		default:
			return buildpackInfo, nil
		}
	}

	return nil, image.ErrNotFound
}

func validateAssetCacheFlags(flags *CreateAssetCacheFlags) error {
	if flags.BuildpackLocator == "" {
		return errors.New("must specify a buildpack locator using the --buildpack flag")
	}
	return nil
}

func getAssets(info *pack.BuildpackInfo) []dist.Asset {
	result := []dist.Asset{}
	assetMap := map[string]dist.Asset{}

	for _, bp := range info.Buildpacks {
		for _, asset := range bp.Assets {
			assetMap[asset.Sha256] = asset
		}
	}

	for _, a := range assetMap {
		result = append(result, a)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

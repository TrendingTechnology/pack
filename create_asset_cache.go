package pack

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/buildpacks/imgutil"
	"github.com/buildpacks/pack/internal/blob"
	"github.com/buildpacks/pack/internal/dist"
	"github.com/buildpacks/pack/pkg/archive"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/pkg/errors"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
)

const AssetLayersLabel = "io.buildpacks.asset.layers"
const AssetMetadataLabel = "io.buildpacks.asset.cache.metadata"

type AssetMetadata map[string]dist.Asset

type BuildpackTOML struct {
	*dist.BuildpackDescriptor
	Assets []dist.Asset `toml:"assets"`
}

type CreateAssetCacheOptions struct {
	ImageName        string
	Assets           []dist.Asset
}

type AssetCacheImage struct {
	Assets []dist.Asset
	AssetMap map[string]blob.Blob
	img      imgutil.Image
}

func NewAssetCacheImage(img imgutil.Image, assetMap map[string]blob.Blob, assets []dist.Asset) *AssetCacheImage {
	return &AssetCacheImage{
		AssetMap: assetMap,
		img:      img,
		Assets: assets,
	}
}

func (c *Client) CreateAssetCache(ctx context.Context, opts CreateAssetCacheOptions) error {
	validOpts, err := validateConfig(opts)
	if err != nil {
		return err
	}

	// TODO -Dan- add support for remote image creation here
	img, err := c.imageFactory.NewImage(validOpts.ImageName, true)
	if err != nil {
		return fmt.Errorf("unable to create asset cache image: %q", err)
	}

	assetMap, err := c.downloadAssets(opts.Assets)
	if err != nil {
		panic(err)
	}

	assetCacheImage := NewAssetCacheImage(img, assetMap, opts.Assets)
	return assetCacheImage.Save()
}

func (a *AssetCacheImage) Save() error {
	tmpDir, err := ioutil.TempDir("", "create-asset-scratch")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	var dstTar *os.File
	assetLabel := AssetMetadata{}
	for _, asset := range a.Assets {
		blob, ok := a.AssetMap[asset.Sha256]
		if !ok {
			panic("associated asset blob does not exist")
		}
		// check permissions bits here....
		{
			// TODO -DAN- audit permission bits here here
			layerPath := filepath.Join(tmpDir, asset.Sha256)
			dstTar, err = os.OpenFile(layerPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.ModePerm)
			defer dstTar.Close()
			if err != nil {
				panic(err)
			}

			// TODO -DAN- use ggcr utilities here to standardize
			hashAlgo := "sha256"
			hash, err := v1.Hasher("sha256")
			if err != nil {
				panic(err)
			}

			w := io.MultiWriter(dstTar, hash)
			tw := tar.NewWriter(w)
			if err = toDistTar(tw, asset.Sha256, blob); err != nil {
				panic(err)
			}
			if err = a.img.AddLayer(layerPath); err != nil {
				panic(err)
			}
			if err = dstTar.Close(); err != nil {
				panic(err)
			}

			asset.LayerDiffID = fmt.Sprintf("%s:%x",hashAlgo,  hash.Sum(nil))
			assetLabel[asset.Sha256] = asset

		}
	}

	assetLabelBuf := bytes.NewBuffer(nil)
	err = json.NewEncoder(assetLabelBuf).Encode(assetLabel)
	if err != nil {
		panic(err)
	}


	err = a.img.SetLabel(AssetLayersLabel, assetLabelBuf.String())
	if err != nil {
		panic(err)
	}

	return a.img.Save()
}

func toDistTar(tw archive.TarWriter, blobSha string, blob dist.Blob) error {
	ts := archive.NormalizedDateTime

	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir,
		Name:     path.Join("cnb"),
		Mode:     0755,
		ModTime:  ts,
	}); err != nil {
		return errors.Wrapf(err, "writing buildpack id dir header")
	}

	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir,
		Name:     path.Join("cnb", "assets"),
		Mode:     0755,
		ModTime:  ts,
	}); err != nil {
		return errors.Wrapf(err, "writing buildpack version dir header")
	}

	buf := bytes.NewBuffer(nil)
	rc, err := blob.Open()
	if err != nil {
		panic(err)
	}
	defer rc.Close()

	_, err = io.Copy(buf, rc)
	if err != nil {
		panic(err)
	}

	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     path.Join("/cnb", "assets", blobSha),
		Mode:     0755,
		Size:     int64(buf.Len()),
		ModTime:  ts,
	}); err != nil {
		return errors.Wrapf(err, "writing buildpack version dir header")
	}

	_, err = tw.Write(buf.Bytes())
	return err
}

func (c *Client) downloadAssets(assets []dist.Asset) (map[string]blob.Blob, error) {
	result := make(map[string]blob.Blob)
	for _, asset := range assets {
		// TODO -Dan- validate the asset before downloading
		b, err := c.downloader.Download(context.Background(), asset.URI, blob.RawOption)
		if err != nil {
			return map[string]blob.Blob{}, err
		}
		result[asset.Sha256] = b
	}
	return result, nil
}

func validateConfig(cfg CreateAssetCacheOptions) (CreateAssetCacheOptions, error) {
	tag, err := name.NewTag(cfg.ImageName, name.WeakValidation)
	if err != nil {
		return CreateAssetCacheOptions{}, fmt.Errorf("invalid asset cache image name: %q", err)
	}
	return CreateAssetCacheOptions{
		ImageName: tag.String(),
	}, nil
}

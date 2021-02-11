package pack_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/buildpacks/imgutil/fakes"
	"github.com/buildpacks/pack"
	"github.com/buildpacks/pack/internal/blob"
	"github.com/buildpacks/pack/internal/dist"
	ilogging "github.com/buildpacks/pack/internal/logging"
	"github.com/buildpacks/pack/logging"
	h "github.com/buildpacks/pack/testhelpers"
	"github.com/buildpacks/pack/testmocks"
	"github.com/golang/mock/gomock"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAssetCacheCommand(t *testing.T) {
	spec.Run(t, "CreateAssetCacheCommand", testCreateAssetCacheCommand, spec.Parallel(), spec.Report(report.Terminal{}))
}

func testCreateAssetCacheCommand(t *testing.T, when spec.G, it spec.S) {
	var (
		client           *pack.Client
		assert           = h.NewAssertionManager(t)
		logger           logging.Logger
		mockController   *gomock.Controller
		mockDownloader   *testmocks.MockDownloader
		mockImageFactory *testmocks.MockImageFactory
		mockImageFetcher *testmocks.MockImageFetcher
		mockDockerClient *testmocks.MockCommonAPIClient
		fakeImage        *fakes.Image
		out              bytes.Buffer
		tmpDir           string
	)
	it.Before(func() {
		var err error
		logger = ilogging.NewLogWithWriters(&out, &out, ilogging.WithVerbose())
		mockController = gomock.NewController(t)
		mockDownloader = testmocks.NewMockDownloader(mockController)
		mockImageFetcher = testmocks.NewMockImageFetcher(mockController)
		mockImageFactory = testmocks.NewMockImageFactory(mockController)
		mockDockerClient = testmocks.NewMockCommonAPIClient(mockController)
		client, err = pack.NewClient(
			pack.WithLogger(logger),
			pack.WithDownloader(mockDownloader),
			pack.WithImageFactory(mockImageFactory),
			pack.WithFetcher(mockImageFetcher),
			pack.WithDockerClient(mockDockerClient),
		)
		assert.Nil(err)

		tmpDir, err = ioutil.TempDir("", "create-asset-cache-command")
		assert.Nil(err)
	})
	when("#CreatAssetCache", func() {
		when("using a local buildpack directory", func() {
			var (
				localBuildpackDir string
				firstAssetBlob    blob.Blob
				secondAssetBlob   blob.Blob
			)

			it.Before(func() {
				firstAssetBlobPath := filepath.Join(tmpDir, "firstAssetBlob")
				assert.Succeeds(ioutil.WriteFile(firstAssetBlobPath, []byte(`
first-asset-blob-contents.
`), os.ModePerm))
				firstAssetBlob = blob.NewBlob(firstAssetBlobPath)

				secondAssetBlobPath := filepath.Join(tmpDir, "firstAssetBlob")
				assert.Succeeds(ioutil.WriteFile(secondAssetBlobPath, []byte(`
second-asset-blob-contents.
`), os.ModePerm))
				secondAssetBlob = blob.NewBlob(secondAssetBlobPath)
			})
			it("succeeds", func() {
				imageName := "test-cache-image"
				imgRef, err := name.NewTag(imageName)
				assert.Nil(err)

				fakeImage = fakes.NewImage(imageName, "somesha256", imgRef)

				mockImageFactory.EXPECT().NewImage(imageName, true).Return(fakeImage, nil)
				mockDownloader.EXPECT().Download(gomock.Any(), "https://first-asset-uri", gomock.Any()).Return(firstAssetBlob, nil)
				mockDownloader.EXPECT().Download(gomock.Any(), "https://second-asset-uri", gomock.Any()).Return(secondAssetBlob, nil)


				assert.Succeeds(client.CreateAssetCache(context.Background(), pack.CreateAssetCacheOptions{
					ImageName:        imageName,
					BuildpackLocator: localBuildpackDir,
					Assets: []dist.Asset{
						{
							ID:      "first-asset",
							Name:    "First Asset",
							Sha256:  "first-sha256",
							Stacks:  []string{"io.buildpacks.stacks.bionic"},
							URI:     "https://first-asset-uri",
							Version: "1.2.3",
						},
						{
							ID:      "second-asset",
							Name:    "Second Asset",
							Sha256:  "second-sha256",
							Stacks:  []string{"io.buildpacks.stacks.bionic"},
							URI:     "https://second-asset-uri",
							Version: "4.5.6",
						},
					},
				}))

				assert.Equal(fakeImage.IsSaved(), true)

				// validate that we added layers
				assert.Equal(fakeImage.NumberOfAddedLayers(), 2)

				//validate layers metadata
				layersLabel, err := fakeImage.Label(pack.AssetLayersLabel)
				assert.Nil(err)

				var assetMetadata pack.AssetMetadata
				assert.Succeeds(json.NewDecoder(strings.NewReader(layersLabel)).Decode(&assetMetadata))
				assert.Equal(assetMetadata, pack.AssetMetadata{
					"first-sha256": dist.Asset {
						ID:      "first-asset",
						Name:    "First Asset",
						LayerDiffID: "sha256:f2ae00cafa3c058df16a0969ce0eb984dc5ea2fe8e71bbdbbd2cdaa3b4359b36",
						Stacks:  []string{"io.buildpacks.stacks.bionic"},
						URI:     "https://first-asset-uri",
						Version: "1.2.3",
					}, "second-sha256": dist.Asset{
						ID:      "second-asset",
						Name:    "Second Asset",
						LayerDiffID: "sha256:46e2287266ceafd2cd4f580566f2b9f504f7b78d472bb3401de18f2410ad1614",
						Stacks:  []string{"io.buildpacks.stacks.bionic"},
						URI:     "https://second-asset-uri",
						Version: "4.5.6",
					},
				})
				

				// TODO: validate that layer contents include `first-asset-blob-contents.` need metadata to access layers first
				firstLayerName, err := fakeImage.FindLayerWithPath("/cnb/assets/first-sha256")
				assert.Nil(err)
				assert.NotEqual(firstLayerName, "")

				secondLayerName, err := fakeImage.FindLayerWithPath("/cnb/assets/second-sha256")
				assert.Nil(err)

				assert.NotEqual(secondLayerName, "")

			})
		})

		when("failure cases", func() {
			when("invalid image name", func() {
				it("fails with an error message", func() {
					imageName := "::::"
					err := client.CreateAssetCache(context.Background(), pack.CreateAssetCacheOptions{
						ImageName:        imageName,
						BuildpackLocator: "fake-locator",
					})
					assert.ErrorContains(err, "invalid asset cache image name: ")
				})
			})
			when("unable to create a new image", func() {
				it("fails with an error message", func() {
					imageName := "some-example-image"
					mockImageFactory.EXPECT().NewImage(imageName, true).Return(nil, errors.New("image fetch error"))

					err := client.CreateAssetCache(context.Background(), pack.CreateAssetCacheOptions{
						ImageName:        imageName,
						BuildpackLocator: "fake-locator",
					})

					assert.ErrorContains(err, "unable to create asset cache image:")
				})
			})
		})
	})
}

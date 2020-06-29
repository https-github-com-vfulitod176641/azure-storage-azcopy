// Copyright © Microsoft <wastore@microsoft.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

// TODO this file was forked from the cmd package, it needs to cleaned to keep only the necessary part

package e2etest

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Azure/azure-storage-azcopy/azbfs"
	minio "github.com/minio/minio-go"

	"github.com/Azure/azure-storage-azcopy/common"
	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/Azure/azure-storage-file-go/azfile"
)

const defaultFileSize = 1024
const defaultStringFileSize = "1k"

type scenarioHelper struct{}

var specialNames = []string{
	"打麻将.txt",
	"wow such space so much space",
	"打%%#%@#%麻将.txt",
	//"saywut.pdf?yo=bla&WUWUWU=foo&sig=yyy", // TODO this breaks on windows, figure out a way to add it only for tests on Unix
	"coração",
	"আপনার নাম কি",
	"%4509%4254$85140&",
	"Donaudampfschifffahrtselektrizitätenhauptbetriebswerkbauunterbeamtengesellschaft",
	"お名前は何ですか",
	"Adın ne",
	"як вас звати",
}

// note: this is to emulate the list-of-files flag
func (scenarioHelper) generateListOfFiles(c asserter, fileList []string) (path string) {
	parentDirName, err := ioutil.TempDir("", "AzCopyLocalTest")
	c.AssertNoErr(err)

	// create the file
	path = common.GenerateFullPath(parentDirName, generateName(c, "listy", 0))
	err = os.MkdirAll(filepath.Dir(path), os.ModePerm)
	c.AssertNoErr(err)

	// pipe content into it
	content := strings.Join(fileList, "\n")
	err = ioutil.WriteFile(path, []byte(content), common.DEFAULT_FILE_PERM)
	c.AssertNoErr(err)
	return
}

func (scenarioHelper) generateLocalDirectory(c asserter) (dstDirName string) {
	dstDirName, err := ioutil.TempDir("", "AzCopyLocalTest")
	c.AssertNoErr(err)
	return
}

// create a test file
func (scenarioHelper) generateLocalFile(filePath string, fileSize int) ([]byte, error) {
	// generate random data
	_, bigBuff := getRandomDataAndReader(fileSize)

	// create all parent directories
	err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm)
	if err != nil {
		return nil, err
	}

	// write to file and return the data
	err = ioutil.WriteFile(filePath, bigBuff, common.DEFAULT_FILE_PERM)
	return bigBuff, err
}

func (s scenarioHelper) generateLocalFilesFromList(c asserter, dirPath string, fileList []*testObject, defaultSize string) {
	for _, file := range fileList {
		var err error
		if file.isFolder() {
			err = os.MkdirAll(filepath.Join(dirPath, file.name), os.ModePerm)
			c.AssertNoErr(err)
			//TODO: nakulkar-msft you'll need to set up things like attributes, and other relevant things from
			//   file.creationProperties here. (Use all the properties of file.creationProperties that are supported
			//			//   by local files. E.g. not contentHeaders or metadata).
		} else {
			_, err = s.generateLocalFile(
				filepath.Join(dirPath, file.name),
				file.creationProperties.sizeBytes(c, defaultSize))
			c.AssertNoErr(err)
			//TODO: nakulkar-msft you'll need to set up things like attributes, and other relevant things from
			//   file.creationProperties here. (Use all the properties of file.creationProperties that are supported
			//   by local files. E.g. not contentHeaders or metadata).
		}
	}

	// sleep a bit so that the files' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
}

// Enumerates all local files and their properties, with the given dirpath
func (s scenarioHelper) enumerateLocalProperties(a asserter, dirpath string) map[string]*objectProperties {
	result := make(map[string]*objectProperties)
	err := filepath.Walk(dirpath, func(fullpath string, info os.FileInfo, err error) error {
		a.AssertNoErr(err) // we don't expect any errors walking the local file system
		relPath := strings.Replace(fullpath, dirpath, "", 1)
		relPath = strings.TrimPrefix(relPath, "\\/")

		size := info.Size()
		lastWriteTime := info.ModTime()
		var pCreationTime *time.Time
		var pSmbAttributes *string
		var pSmbPermissionsSddl *string
		if runtime.GOOS == "windows" {
			var creationTime time.Time
			lastWriteTime, creationTime = osScenarioHelper{}.getFileDates(a, fullpath)
			pCreationTime = &creationTime
			// TODO: nakulkar-msft the osScenarioHelper methods here will need to be implemented before
			//   attribute preservation can be tested. The easiest way to implement them will be to rely on existing AzCopy code
			//   similar to what getFileDates does (see a few lines above)
			/*
				smbAttributes := osScenarioHelper{}.getSmbAttributes(a, fullpath)
				pSmbAttributes = &smbAttributes
				smbPermissionsSddl := osScenarioHelper{}.getSmbSddl(a, fullPath)
				pSmbPermissionsSddl = &smbPermissionsSddl
			*/
		}
		props := objectProperties{
			isFolder:           info.IsDir(),
			size:               &size,
			creationTime:       pCreationTime,
			lastWriteTime:      &lastWriteTime,
			smbAttributes:      pSmbAttributes,
			smbPermissionsSddl: pSmbPermissionsSddl,
			//contentHeaders don't exist on local file system
			//nameValueMetadata doesn't exist on local file system
		}

		result[relPath] = &props
		return nil
	})
	a.AssertNoErr(err)
	return result
}

func (s scenarioHelper) generateCommonRemoteScenarioForLocal(c asserter, dirPath string, prefix string) (fileList []string) {
	fileList = make([]string, 50)
	for i := 0; i < 10; i++ {
		batch := []string{
			generateName(c, prefix+"top", 0),
			generateName(c, prefix+"sub1/", 0),
			generateName(c, prefix+"sub2/", 0),
			generateName(c, prefix+"sub1/sub3/sub5/", 0),
			generateName(c, prefix+specialNames[i], 0),
		}

		for j, name := range batch {
			fileList[5*i+j] = name
			_, err := s.generateLocalFile(filepath.Join(dirPath, name), defaultFileSize)
			c.AssertNoErr(err)
		}
	}

	// sleep a bit so that the files' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
	return
}

// make 50 blobs with random names
// 10 of them at the top level
// 10 of them in sub dir "sub1"
// 10 of them in sub dir "sub2"
// 10 of them in deeper sub dir "sub1/sub3/sub5"
// 10 of them with special characters
func (scenarioHelper) generateCommonRemoteScenarioForBlob(c asserter, containerURL azblob.ContainerURL, prefix string) (blobList []string) {
	blobList = make([]string, 50)

	for i := 0; i < 10; i++ {
		_, blobName1 := createNewBlockBlob(c, containerURL, prefix+"top")
		_, blobName2 := createNewBlockBlob(c, containerURL, prefix+"sub1/")
		_, blobName3 := createNewBlockBlob(c, containerURL, prefix+"sub2/")
		_, blobName4 := createNewBlockBlob(c, containerURL, prefix+"sub1/sub3/sub5/")
		_, blobName5 := createNewBlockBlob(c, containerURL, prefix+specialNames[i])

		blobList[5*i] = blobName1
		blobList[5*i+1] = blobName2
		blobList[5*i+2] = blobName3
		blobList[5*i+3] = blobName4
		blobList[5*i+4] = blobName5
	}

	// sleep a bit so that the blobs' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
	return
}

func (scenarioHelper) generateCommonRemoteScenarioForBlobFS(c asserter, filesystemURL azbfs.FileSystemURL, prefix string) (pathList []string) {
	pathList = make([]string, 50)

	for i := 0; i < 10; i++ {
		_, pathName1 := createNewBfsFile(c, filesystemURL, prefix+"top")
		_, pathName2 := createNewBfsFile(c, filesystemURL, prefix+"sub1/")
		_, pathName3 := createNewBfsFile(c, filesystemURL, prefix+"sub2/")
		_, pathName4 := createNewBfsFile(c, filesystemURL, prefix+"sub1/sub3/sub5")
		_, pathName5 := createNewBfsFile(c, filesystemURL, prefix+specialNames[i])

		pathList[5*i] = pathName1
		pathList[5*i+1] = pathName2
		pathList[5*i+2] = pathName3
		pathList[5*i+3] = pathName4
		pathList[5*i+4] = pathName5
	}

	// sleep a bit so that the paths' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1500)
	return
}

func (scenarioHelper) generateCommonRemoteScenarioForAzureFile(c asserter, shareURL azfile.ShareURL, prefix string) (fileList []string) {
	fileList = make([]string, 50)

	for i := 0; i < 10; i++ {
		_, fileName1 := createNewAzureFile(c, shareURL, prefix+"top")
		_, fileName2 := createNewAzureFile(c, shareURL, prefix+"sub1/")
		_, fileName3 := createNewAzureFile(c, shareURL, prefix+"sub2/")
		_, fileName4 := createNewAzureFile(c, shareURL, prefix+"sub1/sub3/sub5/")
		_, fileName5 := createNewAzureFile(c, shareURL, prefix+specialNames[i])

		fileList[5*i] = fileName1
		fileList[5*i+1] = fileName2
		fileList[5*i+2] = fileName3
		fileList[5*i+3] = fileName4
		fileList[5*i+4] = fileName5
	}

	// sleep a bit so that the blobs' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
	return
}

func (s scenarioHelper) generateBlobContainersAndBlobsFromLists(c asserter, serviceURL azblob.ServiceURL, containerList []string, blobList []*testObject) {
	for _, containerName := range containerList {
		curl := serviceURL.NewContainerURL(containerName)
		_, err := curl.Create(ctx, azblob.Metadata{}, azblob.PublicAccessNone)
		c.AssertNoErr(err)

		s.generateBlobsFromList(c, curl, blobList, defaultStringFileSize)
	}
}

func (s scenarioHelper) generateFileSharesAndFilesFromLists(c asserter, serviceURL azfile.ServiceURL, shareList []string, fileList []*testObject) {
	for _, shareName := range shareList {
		surl := serviceURL.NewShareURL(shareName)
		_, err := surl.Create(ctx, azfile.Metadata{}, 0)
		c.AssertNoErr(err)

		s.generateAzureFilesFromList(c, surl, fileList, defaultStringFileSize)
	}
}

func (s scenarioHelper) generateFilesystemsAndFilesFromLists(c asserter, serviceURL azbfs.ServiceURL, fsList []string, fileList []string, data string) {
	for _, filesystemName := range fsList {
		fsURL := serviceURL.NewFileSystemURL(filesystemName)
		_, err := fsURL.Create(ctx)
		c.AssertNoErr(err)

		s.generateBFSPathsFromList(c, fsURL, fileList)
	}
}

func (s scenarioHelper) generateS3BucketsAndObjectsFromLists(c asserter, s3Client *minio.Client, bucketList []string, objectList []string, data string) {
	for _, bucketName := range bucketList {
		err := s3Client.MakeBucket(bucketName, "")
		c.AssertNoErr(err)

		s.generateObjects(c, s3Client, bucketName, objectList)
	}
}

// create the demanded blobs
func (scenarioHelper) generateBlobsFromList(c asserter, containerURL azblob.ContainerURL, blobList []*testObject, defaultSize string) {
	for _, b := range blobList {
		if b.isFolder() {
			continue // no real folders in blob
		}
		ad := blobResourceAdapter{b}
		blob := containerURL.NewBlockBlobURL(b.name)
		cResp, err := blob.Upload(ctx,
			common.NewRandomDataGenerator(int64(b.creationProperties.sizeBytes(c, defaultSize))),
			ad.toHeaders(),
			ad.toMetadata(),
			azblob.BlobAccessConditions{})
		c.AssertNoErr(err)
		c.Assert(cResp.StatusCode(), equals(), 201)
	}

	// sleep a bit so that the blobs' lmts are guaranteed to be in the past
	// TODO: can we make it so that this sleeping only happens when we really need it to?
	time.Sleep(time.Millisecond * 1050)
}

func (s scenarioHelper) enumerateContainerBlobProperties(a asserter, containerURL azblob.ContainerURL) map[string]*objectProperties {
	result := make(map[string]*objectProperties)

	for marker := (azblob.Marker{}); marker.NotDone(); {

		listBlob, err := containerURL.ListBlobsFlatSegment(context.TODO(), marker, azblob.ListBlobsSegmentOptions{Details: azblob.BlobListingDetails{Metadata: true}})
		a.AssertNoErr(err)

		for _, blobInfo := range listBlob.Segment.BlobItems {

			relativePath := blobInfo.Name // need to change this when we support working on virtual directories down inside containers
			bp := blobInfo.Properties

			h := contentHeaders{
				cacheControl:       bp.CacheControl,
				contentDisposition: bp.ContentDisposition,
				contentEncoding:    bp.ContentEncoding,
				contentLanguage:    bp.ContentLanguage,
				contentType:        bp.ContentType,
				contentMD5:         bp.ContentMD5,
			}
			md := map[string]string(blobInfo.Metadata)

			props := objectProperties{
				isFolder:          false, // no folders in Blob
				size:              bp.ContentLength,
				contentHeaders:    &h,
				nameValueMetadata: md,
				creationTime:      bp.CreationTime,
				lastWriteTime:     &bp.LastModified,
				// smbAttributes and smbPermissions don't exist in blob
			}

			result[relativePath] = &props
		}

		marker = listBlob.NextMarker
	}

	return result
}

func (scenarioHelper) generatePageBlobsFromList(c asserter, containerURL azblob.ContainerURL, blobList []string, data string) {
	for _, blobName := range blobList {
		//Create the blob (PUT blob)
		blob := containerURL.NewPageBlobURL(blobName)
		cResp, err := blob.Create(ctx,
			int64(len(data)),
			0,
			azblob.BlobHTTPHeaders{
				ContentType: "text/random",
			},
			azblob.Metadata{},
			azblob.BlobAccessConditions{},
		)
		c.AssertNoErr(err)
		c.Assert(cResp.StatusCode(), equals(), 201)

		//Create the page (PUT page)
		uResp, err := blob.UploadPages(ctx,
			0,
			strings.NewReader(data),
			azblob.PageBlobAccessConditions{},
			nil,
		)
		c.AssertNoErr(err)
		c.Assert(uResp.StatusCode(), equals(), 201)
	}

	// sleep a bit so that the blobs' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
}

func (scenarioHelper) generateAppendBlobsFromList(c asserter, containerURL azblob.ContainerURL, blobList []string, data string) {
	for _, blobName := range blobList {
		//Create the blob (PUT blob)
		blob := containerURL.NewAppendBlobURL(blobName)
		cResp, err := blob.Create(ctx,
			azblob.BlobHTTPHeaders{
				ContentType: "text/random",
			},
			azblob.Metadata{},
			azblob.BlobAccessConditions{},
		)
		c.AssertNoErr(err)
		c.Assert(cResp.StatusCode(), equals(), 201)

		//Append a block (PUT block)
		uResp, err := blob.AppendBlock(ctx,
			strings.NewReader(data),
			azblob.AppendBlobAccessConditions{},
			nil)
		c.AssertNoErr(err)
		c.Assert(uResp.StatusCode(), equals(), 201)
	}

	// sleep a bit so that the blobs' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
}

func (scenarioHelper) generateBlockBlobWithAccessTier(c asserter, containerURL azblob.ContainerURL, blobName string, accessTier azblob.AccessTierType) {
	blob := containerURL.NewBlockBlobURL(blobName)
	cResp, err := blob.Upload(ctx, strings.NewReader(blockBlobDefaultData), azblob.BlobHTTPHeaders{},
		nil, azblob.BlobAccessConditions{})
	c.AssertNoErr(err)
	c.Assert(cResp.StatusCode(), equals(), 201)

	_, err = blob.SetTier(ctx, accessTier, azblob.LeaseAccessConditions{})
	c.AssertNoErr(err)
}

// create the demanded objects
func (scenarioHelper) generateObjects(c asserter, client *minio.Client, bucketName string, objectList []string) {
	size := int64(len(objectDefaultData))
	for _, objectName := range objectList {
		n, err := client.PutObjectWithContext(ctx, bucketName, objectName, strings.NewReader(objectDefaultData), size, minio.PutObjectOptions{})
		c.AssertNoErr(err)
		c.Assert(n, equals(), size)
	}
}

// create the demanded files
func (scenarioHelper) generateFlatFiles(c asserter, shareURL azfile.ShareURL, fileList []string) {
	for _, fileName := range fileList {
		file := shareURL.NewRootDirectoryURL().NewFileURL(fileName)
		err := azfile.UploadBufferToAzureFile(ctx, []byte(fileDefaultData), file, azfile.UploadToAzureFileOptions{})
		c.AssertNoErr(err)
	}

	// sleep a bit so that the blobs' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
}

// make 50 objects with random names
// 10 of them at the top level
// 10 of them in sub dir "sub1"
// 10 of them in sub dir "sub2"
// 10 of them in deeper sub dir "sub1/sub3/sub5"
// 10 of them with special characters
func (scenarioHelper) generateCommonRemoteScenarioForS3(c asserter, client *minio.Client, bucketName string, prefix string, returnObjectListWithBucketName bool) (objectList []string) {
	objectList = make([]string, 50)

	for i := 0; i < 10; i++ {
		objectName1 := createNewObject(c, client, bucketName, prefix+"top")
		objectName2 := createNewObject(c, client, bucketName, prefix+"sub1/")
		objectName3 := createNewObject(c, client, bucketName, prefix+"sub2/")
		objectName4 := createNewObject(c, client, bucketName, prefix+"sub1/sub3/sub5/")
		objectName5 := createNewObject(c, client, bucketName, prefix+specialNames[i])

		// Note: common.AZCOPY_PATH_SEPARATOR_STRING is added before bucket or objectName, as in the change minimize JobPartPlan file size,
		// transfer.Source & transfer.Destination(after trimed the SourceRoot and DestinationRoot) are with AZCOPY_PATH_SEPARATOR_STRING suffix,
		// when user provided source & destination are without / suffix, which is the case for scenarioHelper generated URL.

		bucketPath := ""
		if returnObjectListWithBucketName {
			bucketPath = common.AZCOPY_PATH_SEPARATOR_STRING + bucketName
		}

		objectList[5*i] = bucketPath + common.AZCOPY_PATH_SEPARATOR_STRING + objectName1
		objectList[5*i+1] = bucketPath + common.AZCOPY_PATH_SEPARATOR_STRING + objectName2
		objectList[5*i+2] = bucketPath + common.AZCOPY_PATH_SEPARATOR_STRING + objectName3
		objectList[5*i+3] = bucketPath + common.AZCOPY_PATH_SEPARATOR_STRING + objectName4
		objectList[5*i+4] = bucketPath + common.AZCOPY_PATH_SEPARATOR_STRING + objectName5
	}

	// sleep a bit so that the blobs' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
	return
}

// create the demanded azure files
func (scenarioHelper) generateAzureFilesFromList(c asserter, shareURL azfile.ShareURL, fileList []*testObject, defaultSize string) {
	for _, f := range fileList {
		ad := filesResourceAdapter{f}
		if f.isFolder() {
			// make sure the dir exists
			file := shareURL.NewRootDirectoryURL().NewFileURL(path.Join(f.name, "dummyChild"))
			generateParentsForAzureFile(c, file)

			// set its metadata if any
			if f.creationProperties.nameValueMetadata != nil {
				dir := shareURL.NewRootDirectoryURL().NewDirectoryURL(f.name)
				_, err := dir.SetMetadata(context.TODO(), ad.toMetadata())
				c.AssertNoErr(err)
			}

			// set other properties
			// TODO: do we need a SetProperties method on dir...?  Discuss with zezha-msft
			if f.creationProperties.creationTime != nil ||
				f.creationProperties.smbPermissionsSddl != nil ||
				f.creationProperties.smbAttributes != nil {
				panic("setting these properties isn't implmented yet for folders in the test harnesss")
				// TODO: nakulkar-msft the attributes stuff will need to be implemented here before attributes can be tested on Azure Files
			}
			// TODO: I'm pretty sure we don't prserve lastWritetime or contentProperties (headers) for folders, so the above if statement doesn't test those
			//    Is that the correct decision?
		} else {
			file := shareURL.NewRootDirectoryURL().NewFileURL(f.name)

			// create parents first
			generateParentsForAzureFile(c, file)

			// create the file itself
			cResp, err := file.Create(ctx, int64(f.creationProperties.sizeBytes(c, defaultSize)), ad.toHeaders(), ad.toMetadata())
			c.AssertNoErr(err)
			c.Assert(cResp.StatusCode(), equals(), 201)

			// TODO: do we want to put some random content into it?
		}
	}

	// sleep a bit so that the files' lmts are guaranteed to be in the past
	time.Sleep(time.Millisecond * 1050)
}

func (s scenarioHelper) enumerateShareFileProperties(a asserter, shareURL azfile.ShareURL) map[string]*objectProperties {
	a.AssertNoErr(errors.New("enumerateShareFileProperties is not yet implemeted"))
	//root := shareURL.NewRootDirectoryURL()
	// TODO use root.ListFilesAndDirectoriesSegment()
	// TODO: nakulkar-msft ?
	return nil
}

func (scenarioHelper) generateBFSPathsFromList(c asserter, filesystemURL azbfs.FileSystemURL, fileList []string) {
	for _, path := range fileList {
		file := filesystemURL.NewRootDirectoryURL().NewFileURL(path)

		// Create the file
		cResp, err := file.Create(ctx, azbfs.BlobFSHTTPHeaders{})
		c.AssertNoErr(err)
		c.Assert(cResp.StatusCode(), equals(), 201)

		aResp, err := file.AppendData(ctx, 0, strings.NewReader(string(make([]byte, defaultBlobFSFileSizeInBytes))))
		c.AssertNoErr(err)
		c.Assert(aResp.StatusCode(), equals(), 202)

		fResp, err := file.FlushData(ctx, defaultBlobFSFileSizeInBytes, nil, azbfs.BlobFSHTTPHeaders{}, false, true)
		c.AssertNoErr(err)
		c.Assert(fResp.StatusCode(), equals(), 200)
	}
}

// Golang does not have sets, so we have to use a map to fulfill the same functionality
func (scenarioHelper) convertListToMap(list []*testObject, converter func(*testObject) string) map[string]int {
	lookupMap := make(map[string]int)
	for _, entry := range list {
		entryName := converter(entry)
		lookupMap[entryName] = 0
	}

	return lookupMap
}

func (scenarioHelper) shaveOffPrefix(list []string, prefix string) []string {
	cleanList := make([]string, len(list))
	for i, item := range list {
		cleanList[i] = strings.TrimPrefix(item, prefix)
	}
	return cleanList
}

func (scenarioHelper) addPrefix(list []string, prefix string) []string {
	modifiedList := make([]string, len(list))
	for i, item := range list {
		modifiedList[i] = prefix + item
	}
	return modifiedList
}

func (scenarioHelper) getRawContainerURLWithSAS(c asserter, containerName string) url.URL {
	accountName, accountKey := GlobalInputManager{}.GetAccountAndKey(EAccountType.Standard())
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	c.AssertNoErr(err)
	containerURLWithSAS := getContainerURLWithSAS(c, *credential, containerName)
	return containerURLWithSAS.URL()
}

func (scenarioHelper) getRawBlobURLWithSAS(c asserter, containerName string, blobName string) url.URL {
	accountName, accountKey := GlobalInputManager{}.GetAccountAndKey(EAccountType.Standard())
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	c.AssertNoErr(err)
	containerURLWithSAS := getContainerURLWithSAS(c, *credential, containerName)
	blobURLWithSAS := containerURLWithSAS.NewBlockBlobURL(blobName)
	return blobURLWithSAS.URL()
}

func (scenarioHelper) getRawBlobServiceURLWithSAS(c asserter) url.URL {
	accountName, accountKey := GlobalInputManager{}.GetAccountAndKey(EAccountType.Standard())
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	c.AssertNoErr(err)

	return getBlobServiceURLWithSAS(c, *credential).URL()
}

func (scenarioHelper) getRawFileServiceURLWithSAS(c asserter) url.URL {
	accountName, accountKey := GlobalInputManager{}.GetAccountAndKey(EAccountType.Standard())
	credential, err := azfile.NewSharedKeyCredential(accountName, accountKey)
	c.AssertNoErr(err)

	return getFileServiceURLWithSAS(c, *credential).URL()
}

func (scenarioHelper) getRawAdlsServiceURLWithSAS(c asserter) azbfs.ServiceURL {
	accountName, accountKey := GlobalInputManager{}.GetAccountAndKey(EAccountType.Standard())
	credential := azbfs.NewSharedKeyCredential(accountName, accountKey)

	return getAdlsServiceURLWithSAS(c, *credential)
}

func (scenarioHelper) getBlobServiceURL(c asserter) azblob.ServiceURL {
	accountName, accountKey := GlobalInputManager{}.GetAccountAndKey(EAccountType.Standard())
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	c.AssertNoErr(err)
	rawURL := fmt.Sprintf("https://%s.blob.core.windows.net", credential.AccountName())

	// convert the raw url and validate it was parsed successfully
	fullURL, err := url.Parse(rawURL)
	c.AssertNoErr(err)

	return azblob.NewServiceURL(*fullURL, azblob.NewPipeline(credential, azblob.PipelineOptions{}))
}

func (s scenarioHelper) getContainerURL(c asserter, containerName string) azblob.ContainerURL {
	serviceURL := s.getBlobServiceURL(c)
	containerURL := serviceURL.NewContainerURL(containerName)

	return containerURL
}

func (scenarioHelper) getRawS3AccountURL(c asserter, region string) url.URL {
	rawURL := fmt.Sprintf("https://s3%s.amazonaws.com", common.IffString(region == "", "", "-"+region))

	fullURL, err := url.Parse(rawURL)
	c.AssertNoErr(err)

	return *fullURL
}

// TODO: Possibly add virtual-hosted-style and dual stack support. Currently use path style for testing.
func (scenarioHelper) getRawS3BucketURL(c asserter, region string, bucketName string) url.URL {
	rawURL := fmt.Sprintf("https://s3%s.amazonaws.com/%s", common.IffString(region == "", "", "-"+region), bucketName)

	fullURL, err := url.Parse(rawURL)
	c.AssertNoErr(err)

	return *fullURL
}

func (scenarioHelper) getRawS3ObjectURL(c asserter, region string, bucketName string, objectName string) url.URL {
	rawURL := fmt.Sprintf("https://s3%s.amazonaws.com/%s/%s", common.IffString(region == "", "", "-"+region), bucketName, objectName)

	fullURL, err := url.Parse(rawURL)
	c.AssertNoErr(err)

	return *fullURL
}

func (scenarioHelper) getRawFileURLWithSAS(c asserter, shareName string, fileName string) url.URL {
	credential, err := getGenericCredentialForFile("")
	c.AssertNoErr(err)
	shareURLWithSAS := getShareURLWithSAS(c, *credential, shareName)
	fileURLWithSAS := shareURLWithSAS.NewRootDirectoryURL().NewFileURL(fileName)
	return fileURLWithSAS.URL()
}

func (scenarioHelper) getRawShareURLWithSAS(c asserter, shareName string) url.URL {
	accountName, accountKey := GlobalInputManager{}.GetAccountAndKey(EAccountType.Standard())
	credential, err := azfile.NewSharedKeyCredential(accountName, accountKey)
	c.AssertNoErr(err)
	shareURLWithSAS := getShareURLWithSAS(c, *credential, shareName)
	return shareURLWithSAS.URL()
}

func (scenarioHelper) blobExists(blobURL azblob.BlobURL) bool {
	_, err := blobURL.GetProperties(context.Background(), azblob.BlobAccessConditions{})
	if err == nil {
		return true
	}
	return false
}

func (scenarioHelper) containerExists(containerURL azblob.ContainerURL) bool {
	_, err := containerURL.GetProperties(context.Background(), azblob.LeaseAccessConditions{})
	if err == nil {
		return true
	}
	return false
}

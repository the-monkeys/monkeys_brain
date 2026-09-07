package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_storage/constant"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_storage/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_storage/internal/utils"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type FileService struct {
	path           string
	profilePicPath string
	log            *zap.SugaredLogger
	db             database.StorageDB
	pb.UnimplementedUploadBlogFileServer
}

func NewFileService(path, profilePic string, db database.StorageDB, log *zap.SugaredLogger) *FileService {
	return &FileService{path: path, profilePicPath: profilePic, db: db, log: log}
}

func (fs *FileService) UploadBlogFile(stream pb.UploadBlogFile_UploadBlogFileServer) error {
	var byteSlice []byte
	var blogID, fileName string
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		byteSlice = append(byteSlice, chunk.Data...)
		blogID = chunk.BlogId
		fileName = chunk.FileName
	}
	fs.log.Debugf("Uploading a file for blog id: %v", blogID)

	fileName = utils.RemoveSpecialChar(fileName)
	dirPath, filePath := utils.ConstructPath(fs.path, blogID, fileName)

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		fs.log.Debugf("the directory, %s doesn't exists", dirPath)

		if err := os.MkdirAll(dirPath, 0755); err != nil {
			fs.log.Errorf("cannot create a directory for this blog id: %s", blogID)
			return err
		}
	}

	if err := os.WriteFile(filePath, byteSlice, 0644); err != nil {
		fs.log.Errorf("cannot write file for blog id: %s, error: %v", blogID, err)
		return err
	}

	fs.log.Debugf("done uploading file: %s", filePath)
	return stream.SendAndClose(&pb.UploadBlogFileRes{
		Status:      http.StatusOK,
		NewFileName: fileName,
	})
}

func (fs *FileService) GetBlogFile(req *pb.GetBlogFileReq, stream pb.UploadBlogFile_GetBlogFileServer) error {
	fileName := filepath.Join(fs.path, req.BlogId, req.FileName)
	fs.log.Debugf("there is a request to retrieve the file, %s", fileName)

	_, err := os.Stat(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			return status.Error(codes.NotFound, fmt.Sprintf("image for blog id %v not found", req.BlogId))
		}
		return status.Errorf(codes.Internal, "something went wrong")
	}

	rawFileName := strings.ReplaceAll(fileName, "\n", "")
	fileBytes, err := os.ReadFile(rawFileName)
	if err != nil {
		fs.log.Errorf("cannot read the file: %s, error: %v", fileName, fileBytes)
		return err
	}

	if err := stream.Send(&pb.GetBlogFileRes{
		Data: fileBytes,
	}); err != nil {
		fs.log.Errorf("error while sending stream, error %+v", err)
	}

	return nil
}

func (fs *FileService) DeleteBlogFile(ctx context.Context, req *pb.DeleteBlogFileReq) (*pb.DeleteBlogFileRes, error) {
	filePath := filepath.Join(fs.path, req.BlogId, req.FileName)

	fs.log.Debugf("there is a request to delete the file, %s", filePath)

	if err := os.Remove(filePath); err != nil {
		return nil, err
	}

	return &pb.DeleteBlogFileRes{
		Message: "successfully deleted",
		Status:  http.StatusOK,
	}, nil
}

func (fs *FileService) UploadProfilePic(stream pb.UploadBlogFile_UploadProfilePicServer) error {
	fs.log.Debugf("File server got request to save profile pic")
	var byteSlice []byte
	var userName string

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		byteSlice = append(byteSlice, chunk.Data...)
		userName = chunk.UserId
	}
	fs.log.Debugf("Uploading a file for user id: %v", userName)

	fileName := "profile.png"
	dirPath, filePath := utils.ConstructPath(fs.profilePicPath, userName, fileName)

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		fs.log.Debugf("the directory, %s doesn't exists", dirPath)

		if err := os.MkdirAll(dirPath, 0755); err != nil {
			fs.log.Errorf("cannot create a directory for this blog id: %s", userName)
			return err
		}
	}

	if err := os.WriteFile(filePath, byteSlice, 0644); err != nil {
		fs.log.Errorf("cannot create a file for this blog id: %s", userName)
		return err
	}

	fs.log.Debugf("done uploading profile pic: %s", filePath)
	return stream.SendAndClose(&pb.UploadProfilePicRes{
		Status:   http.StatusOK,
		FileName: fileName,
	})
}

func (fs *FileService) GetProfilePic(req *pb.GetProfilePicReq, stream pb.UploadBlogFile_GetProfilePicServer) error {
	fileName := filepath.Join(fs.profilePicPath, req.UserId, req.FileName)

	_, err := os.Stat(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			return status.Error(codes.NotFound, fmt.Sprintf("profile picture for %v not found", req.UserId))
		}
		return status.Errorf(codes.Internal, "something went wrong")
	}

	fileBytes, err := os.ReadFile(fileName)
	if err != nil {
		fs.log.Errorf("cannot read the profile pic: %s, error: %v", fileName, fileBytes)
		return err
	}

	if err := stream.Send(&pb.GetProfilePicRes{
		Data: fileBytes,
	}); err != nil {
		fs.log.Errorf("error while sending profile pic stream, error %+v", err)
	}

	return nil
}

func (fs *FileService) DeleteProfilePic(ctx context.Context, req *pb.DeleteProfilePicReq) (*pb.DeleteProfilePicRes, error) {
	fs.log.Debugf("File server got request to delete profile pic")
	filePath := filepath.Join(fs.profilePicPath, req.UserId, req.FileName)
	fs.log.Debugf("there is a request to delete the profile pic for user, %s", req.UserId)
	if err := os.Remove(filePath); err != nil {
		fs.log.Errorf("Error while removing profile image for user: %s, error: %v", req.UserId, err)
		return nil, status.Errorf(codes.Internal, "Something went wrong while removing file")
	}
	imageByte, err := utils.ReadImageFromURL(constant.DefaultProfilePhoto)
	if err != nil {
		fs.log.Errorf("Error fetching default profile image for user: %s, error: %v", req.UserId, err)
		return nil, status.Errorf(codes.Internal, "error fetching image")
	}
	if err = os.WriteFile(filePath, imageByte, 0644); err != nil {
		fs.log.Errorf("Cannot write default profile image file for user: %s, error: %v", req.UserId, err)
		return nil, status.Errorf(codes.Internal, "error writing default profile image")
	}
	fs.log.Debugf("Done resetting default profile pic: %s", filePath)

	return &pb.DeleteProfilePicRes{
		Message: "successfully deleted",
		Status:  http.StatusOK,
	}, nil
}

func (fs *FileService) CheckAsset(ctx context.Context, req *pb.CheckAssetReq) (*pb.CheckAssetRes, error) {
	res, err := fs.db.CheckAsset(ctx, req.Checksum)
	if err != nil {
		fs.log.Errorf("failed to check asset: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to check asset")
	}
	return res, nil
}

func (fs *FileService) RegisterAsset(ctx context.Context, req *pb.RegisterAssetReq) (*pb.RegisterAssetRes, error) {
	res, err := fs.db.RegisterAsset(ctx, req)
	if err != nil {
		fs.log.Errorf("failed to register asset: %v", err)
		return &pb.RegisterAssetRes{Success: false, Error: err.Error()}, nil
	}

	return res, nil
}

func (fs *FileService) UpdateNSFW(ctx context.Context, req *pb.UpdateNSFWReq) (*pb.UpdateNSFWRes, error) {
	err := fs.db.UpdateNSFW(ctx, req.Checksum, req.IsNsfw, req.NsfwScore)
	if err != nil {
		fs.log.Errorf("failed to update NSFW: %v", err)
		return &pb.UpdateNSFWRes{Success: false, Error: err.Error()}, nil
	}

	return &pb.UpdateNSFWRes{Success: true}, nil
}

func (fs *FileService) CreateAssetRef(ctx context.Context, req *pb.CreateAssetRefReq) (*pb.CreateAssetRefRes, error) {
	res, err := fs.db.CreateAssetRef(ctx, req)
	if err != nil {
		fs.log.Errorf("failed to create asset ref: %v", err)
		return &pb.CreateAssetRefRes{Success: false, Error: err.Error()}, nil
	}

	return res, nil
}

func (fs *FileService) DeleteAssetRef(ctx context.Context, req *pb.DeleteAssetRefReq) (*pb.DeleteAssetRefRes, error) {
	res, err := fs.db.DeleteAssetRef(ctx, req)
	if err != nil {
		fs.log.Errorf("failed to delete asset ref: %v", err)
		return &pb.DeleteAssetRefRes{Success: false, Error: err.Error()}, nil
	}

	return res, nil
}

func (fs *FileService) ReplaceAssetRef(ctx context.Context, req *pb.ReplaceAssetRefReq) (*pb.ReplaceAssetRefRes, error) {
	res, err := fs.db.ReplaceAssetRef(ctx, req)
	if err != nil {
		fs.log.Errorf("failed to replace asset ref: %v", err)
		return &pb.ReplaceAssetRefRes{Success: false, Error: err.Error()}, nil
	}

	return res, nil
}

func (fs *FileService) ResolveAssetRead(ctx context.Context, req *pb.ResolveAssetReadReq) (*pb.ResolveAssetReadResp, error) {
	res, err := fs.db.ResolveAssetRead(ctx, req)
	if err != nil {
		fs.log.Errorf("failed to resolve asset read: %v", err)
		return &pb.ResolveAssetReadResp{NotFound: true}, nil
	}
	return res, nil
}

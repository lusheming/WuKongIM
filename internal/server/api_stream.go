package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type StreamAPI struct {
	s *Server
	wklog.Log
}

func NewStreamAPI(s *Server) *StreamAPI {
	return &StreamAPI{
		s:   s,
		Log: wklog.NewWKLog("StreamAPI"),
	}
}

// Route route
func (s *StreamAPI) Route(r *wkhttp.WKHttp) {
	r.POST("/stream/start", s.start) // 流消息开始
	r.POST("/stream/end", s.end)     // 流消息结束
}

func (s *StreamAPI) start(c *wkhttp.Context) {
	var req streamStartReq
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.FromUid) == "" {
		req.FromUid = s.s.opts.SystemUID
	}
	channelId := req.ChannelId
	channelType := req.ChannelType
	if strings.TrimSpace(channelId) == "" { //指定了频道 正常发送
		s.Error("无法处理发送消息请求！", zap.Any("req", req))
		c.ResponseError(errors.New("无法处理发送消息请求！"))
		return
	}

	fakeChannelId := channelId
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(channelId, req.FromUid)
	}

	clientMsgNo := req.ClientMsgNo
	if strings.TrimSpace(clientMsgNo) == "" {
		clientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}
	streamNo := wkutil.GenUUID()

	messageId := s.s.channelReactor.messageIDGen.Generate().Int64()

	msg := wkdb.Message{
		RecvPacket: wkproto.RecvPacket{
			Framer: wkproto.Framer{
				RedDot:    wkutil.IntToBool(req.Header.RedDot),
				SyncOnce:  wkutil.IntToBool(req.Header.SyncOnce),
				NoPersist: wkutil.IntToBool(req.Header.NoPersist),
			},
			MessageID:   messageId,
			ClientMsgNo: clientMsgNo,
			FromUID:     req.FromUid,
			ChannelID:   channelId,
			ChannelType: channelType,
			Timestamp:   int32(time.Now().Unix()),
			StreamNo:    streamNo,
			Payload:     req.Payload,
		},
	}

	// 保存消息
	timeoutCtx, cancel := context.WithTimeout(s.s.ctx, time.Second*5)
	results, err := s.s.store.AppendMessages(timeoutCtx, fakeChannelId, channelType, []wkdb.Message{
		msg,
	})
	cancel()
	if err != nil {
		s.Error("保存消息失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if len(results) == 0 {
		s.Error("保存消息失败！没返回结果", zap.Error(errors.New("保存消息失败")))
		c.ResponseError(errors.New("保存消息失败"))
		return
	}
	result := results[0]

	// 添加流元数据
	streamMeta := &wkdb.StreamMeta{
		StreamNo:    streamNo,
		ChannelId:   channelId,
		ChannelType: channelType,
		FromUid:     req.FromUid,
		ClientMsgNo: clientMsgNo,
		MessageId:   messageId,
		MessageSeq:  int64(result.LogIndex()),
	}
	err = s.s.store.AddStreamMeta(streamMeta)
	if err != nil {
		s.Error("添加流元数据失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 提按消息发送
	err = s.s.channelReactor.proposeSend(messageId, req.FromUid, s.s.opts.SystemDeviceId, SystemConnId, s.s.opts.Cluster.NodeId, false, &wkproto.SendPacket{
		Framer: wkproto.Framer{
			RedDot:    wkutil.IntToBool(req.Header.RedDot),
			SyncOnce:  wkutil.IntToBool(req.Header.SyncOnce),
			NoPersist: wkutil.IntToBool(req.Header.NoPersist),
		},
		StreamNo:    streamNo,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelId,
		ChannelType: channelType,
		Payload:     req.Payload,
	})
	if err != nil {
		s.Error("提按发送失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"stream_no": streamNo,
	})

}

func (s *StreamAPI) end(c *wkhttp.Context) {

}

type streamStartReq struct {
	Header      MessageHeader `json:"header"`        // 消息头
	ClientMsgNo string        `json:"client_msg_no"` // 客户端消息编号（相同编号，客户端只会显示一条）
	FromUid     string        `json:"from_uid"`      // 发送者UID
	ChannelId   string        `json:"channel_id"`    // 频道ID
	ChannelType uint8         `json:"channel_type"`  // 频道类型
	Payload     []byte        `json:"payload"`       // 消息内容
}

type streamEndReq struct {
	StreamNo    string `json:"stream_no"`    // 消息流编号
	ChannelId   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
}
package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type Handler struct {
	wklog.Log
}

func NewHandler() *Handler {
	h := &Handler{
		Log: wklog.NewWKLog("handler"),
	}
	h.routes()
	return h
}

func (h *Handler) routes() {
	// 连接事件
	eventbus.RegisterUserHandlers(eventbus.EventConnect, h.connect)
	// 连接回执
	eventbus.RegisterUserHandlers(eventbus.EventConnack, h.connack)
	// 发送事件
	eventbus.RegisterUserHandlers(eventbus.EventOnSend, h.onSend)
	// 连接写事件
	eventbus.RegisterUserHandlers(eventbus.EventConnWriteFrame, h.writeFrame)
	// 连接关闭
	eventbus.RegisterUserHandlers(eventbus.EventConnClose, h.closeConn)

}

// 收到消息
func (h *Handler) OnMessage(m *proto.Message) {
	switch msgType(m.MsgType) {
	case msgForwardUserEvent:
		h.onForwardUserEvent(m)
	}
}

// 收到事件
func (h *Handler) OnEvent(ctx *eventbus.UserContext) {
	slotLeaderId := h.userLeaderNodeId(ctx.Uid)
	if slotLeaderId == 0 {
		h.Error("OnEvent: get slotLeaderId is 0")
		return
	}
	if options.G.IsLocalNode(slotLeaderId) || h.notForwardToLeader(ctx.EventType) {
		// 执行本地事件
		eventbus.ExecuteUserEvent(ctx)
	} else {
		// 转发到leader节点
		h.forwardsToNode(slotLeaderId, ctx.Uid, ctx.Events)
	}
}

// 不需要转发给领导的事件
func (h *Handler) notForwardToLeader(eventType eventbus.EventType) bool {
	switch eventType {
	case eventbus.EventConnClose,
		eventbus.EventConnack,
		eventbus.EventConnWriteFrame:
		return true
	}
	return false

}

// 获得用户的leader节点
func (h *Handler) userLeaderNodeId(uid string) uint64 {
	slotId := service.Cluster.GetSlotId(uid)
	leaderId, err := service.Cluster.SlotLeaderId(slotId)
	if err != nil {
		h.Error("getUserLeaderNodeId: get leaderId failed", zap.Error(err))
		return 0
	}
	return leaderId
}

func (h *Handler) forwardsToNode(nodeId uint64, uid string, events []*eventbus.Event) {
	if len(events) == 0 {
		return
	}

	req := &forwardUserEventReq{
		uid:      uid,
		fromNode: options.G.Cluster.NodeId,
		events:   events,
	}
	data, err := req.encode()
	if err != nil {
		h.Error("forwardToLeader: encode failed", zap.Error(err))
		return
	}
	msg := &proto.Message{
		MsgType: uint32(msgForwardUserEvent),
		Content: data,
	}
	err = h.sendToNode(nodeId, msg)
	if err != nil {
		h.Error("forwardToLeader: send failed", zap.Error(err))
		return
	}
}

func (h *Handler) forwardToNode(nodeId uint64, uid string, event *eventbus.Event) {
	h.forwardsToNode(nodeId, uid, []*eventbus.Event{event})
}

func (h *Handler) sendToNode(toNodeId uint64, msg *proto.Message) error {
	err := service.Cluster.Send(toNodeId, msg)
	return err
}

// 收到转发用户事件
func (h *Handler) onForwardUserEvent(m *proto.Message) {
	req := &forwardUserEventReq{}
	err := req.decode(m.Content)
	if err != nil {
		h.Error("onForwardUserEvent: decode failed", zap.Error(err))
		return
	}
	slotLeaderId := h.userLeaderNodeId(req.uid)
	if slotLeaderId == 0 {
		h.Error("OnEvent: get slotLeaderId is 0")
		return
	}

	isSlotLeader := options.G.IsLocalNode(slotLeaderId)

	for _, e := range req.events {
		if !h.notForwardToLeader(e.Type) {
			if !isSlotLeader {
				h.Error("onForwardUserEvent: event type is not EventConnWriteFrame, but not slot leader", zap.String("uid", req.uid), zap.Uint64("slotLeaderId", slotLeaderId))
				continue
			}
		}
		eventbus.User.AddEvent(req.uid, e)
	}
	eventbus.User.Advance(req.uid)

}
// Package wecom 企业微信渠道（预留）。
//
// 一期只占位：设置分区 channel.wecom 已定义（CorpID / AgentID / Secret / Token / EncodingAESKey），
// settings.Validate 禁止启用。实装时实现 channel.WebhookAdapter 即可接入，编排器无需改动：
//
//  1. ServeWebhook：GET 做 URL 验证（msg_signature 校验 + AES 解密 echostr 原样返回）；
//     POST 收加密 XML，解密后把文本消息翻译成 channel.Inbound（ChatType=private，ChatID=FromUserName；
//     群机器人场景 ChatType=group）。须在 5 秒内响应空串，处理异步进行。
//  2. Send：gettoken（corpid+secret，7200s 缓存）→ POST /cgi-bin/message/send（touser + agentid + text）。
//     企业微信没有「被动回复 5 分钟窗口」，Outbound.ReplyTo 忽略。
//  3. 在 runtime.buildAdapters 里按 settings.WeCom.Enabled 装配，并删除 Validate 中的禁用分支。
package wecom

export type RequirementSuggestion = { key:string; title:string; detail:string }

// 仅在本机按可解释的规则提示遗漏，不推断业务结论，不向外部模型发送内容。
export function requirementSuggestions(description:string, acceptance=''):RequirementSuggestion[] {
 const text=description.slice(0,120000).replace(/```[\s\S]*?```/g,'').trim()
 if(!text)return []
 const all=text+'\n'+acceptance.slice(0,30000), result:RequirementSuggestion[]=[]
 const add=(key:string,title:string,detail:string)=>result.push({key,title,detail})
 if(text.length<40)add('detail','补充具体操作场景','描述较短，建议写清谁在什么页面、遇到什么问题、执行什么操作以及期望结果。')
 if(!/(用户|客户|管理员|访客|角色|人员|工程师|产品经理|user|admin|customer)/i.test(text))add('audience','明确使用人群','当前描述未识别到目标用户或角色，建议注明使用人群及使用前提。')
 if(!/(背景|目标|解决|痛点|为了|提升|降低|减少|避免|目的|goal|problem|because)/i.test(text))add('goal','说明业务目标','建议补充当前问题及本次改动希望带来的业务结果，便于研发判断范围。')
 if(!acceptance.trim()&&!/(验收|预期|期望|应当|应该|必须|则显示|then|expected|acceptance)/i.test(text))add('acceptance','补充可验证的验收标准','建议按“前置条件 → 操作 → 可观察结果”列出验收步骤，明确什么情况下算完成。')
 if(!/(异常|失败|为空|空值|无结果|超时|重复|边界|错误|error|empty|timeout|duplicate)/i.test(all))add('edge','补充异常与边界','尚未识别到异常说明，建议补充空数据、重复操作、网络失败等适用场景及对应提示。')
 if(/(优化|快速|及时|友好|美观|高效|尽快|流畅|fast|quick|friendly)/i.test(all)&&!/(\d+\s*(ms|毫秒|秒|分钟|%|条|个|px)|不超过|至少|至多|小于|大于|验收截图)/i.test(all))add('measurable','将模糊表述改成可验收条件','检测到“优化、快速、友好”等表述，建议补充可验证的响应时间、数量限制或界面参考，数值由业务确认。')
 if(/(删除|导出|下载|权限|登录|账号|支付|delete|export|login)/i.test(text)&&!/(授权|可见范围|管理员|审计|确认|恢复|越权|认证|permission|authorization)/i.test(all))add('permission','明确权限与安全边界','涉及删除、导出或账号等操作，建议说明谁可操作、数据范围，以及是否需要确认、审计或恢复。')
 return result
}

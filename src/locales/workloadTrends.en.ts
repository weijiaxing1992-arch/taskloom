export default {
 '筛选成员（含历史成员）':'Filter members (including historical members)','仅筛选统计记录，不变更工作项人员绑定。':'Filters statistics only; does not change work item assignments.',
 '工作量趋势':'Workload trends','刷新趋势':'Refresh trends','趋势分组':'Trend grouping','按月份':'By month','按迭代版本':'By iteration','统计指标':'Metric','时间范围':'Time range','最近 6 个月':'Last 6 months','最近 12 个月':'Last 12 months','所有成员':'All members','趋势上下文校验失败，请重试':'The trend context could not be verified. Please retry.','趋势暂时无法读取，请重试':'Trends could not be loaded. Please retry.','正在汇总趋势…':'Aggregating trends…','当前快照':'Current snapshot','无数据':'No data',
 '按迭代结束月份归属的当前快照，不代表历史当月实际完成或上线。':'Current snapshots grouped by iteration end month, not historical completion or release events.',
 '趋势筛选仅影响本图和下方数据表；部门、成员与职能采用月度明细相同的归属和分摊口径。':'These filters affect only this chart and its table. Department, member and discipline attribution use the same rules as the monthly details.',
 '当前范围没有可绘制的数据；未估算权重不会伪装成已填的零。':'No plottable data in this range. Unestimated weights are not shown as explicit zero estimates.',
 '横向滚动查看趋势图':'Scroll horizontally through the trend chart','{metric}趋势，共 {count} 个分组':'{metric} trend across {count} groups',
 '无数据或未估算位置断开连线，真实零值显示在基线上；下方表格提供精确数值。':'Lines break where data or estimates are missing. Explicit zero values appear on the baseline. Exact values are available in the table below.',
 '悬停或聚焦数据点查看精确数值。':'Hover or focus a data point for its exact value.','第 {page}/{pages} 组，每组最多 12 个迭代':'Group {page}/{pages}, up to 12 iterations per group','查看精确趋势数据':'View exact trend data','精确趋势数据表':'Exact trend data table','月份':'Month','迭代版本':'Iteration version','项目 / 结束日期':'Project / end date','数据状态':'Data availability','无匹配工作项':'No matching work items','{count} 条需求 · {gaps} 项职能未估算':'{count} requirements · {gaps} unestimated discipline entries',
 '按迭代日期排序；跨项目同名迭代按项目与迭代 ID 分开。没有匹配工作项显示无数据，有已填权重且总和为零才显示 0。':'Iterations are sorted by end date and identified by project and iteration ID, even when names match. Missing work is shown as no data; 0 means explicit estimates sum to zero.',
} satisfies Record<string,string>

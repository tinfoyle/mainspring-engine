export function ProductPreview() {
  return <div className="product-preview" aria-label="Preview of the Spyglass operating workspace">
    <div className="preview-top"><div className="preview-brand"><span className="brand-mark mini"><i /></span><b>Spyglass</b></div><div className="preview-search">⌕ <span>Ask Spyglass anything...</span><kbd>⌘ K</kbd></div><div className="preview-person">AJ</div></div>
    <div className="preview-body">
      <aside className="preview-nav"><p>OPERATE</p><b><i /> Overview</b><span>◫ Work <em>12</em></span><span>◎ Your turn <em className="amber">3</em></span><p>CAPABILITIES</p><span>✦ Agents</span><span>▤ Knowledge</span><span>◒ Finance</span><span>◇ Marketing</span></aside>
      <section className="preview-main"><header><div><small>MONDAY, AUGUST 17</small><h2>Good morning, Avery.</h2><p>Here is what is moving across Northstar Studio.</p></div><span className="live"><i /> Systems live</span></header>
        <div className="preview-metrics"><article><small>ACTIVE WORK</small><strong>12</strong><span className="up">↑ 4 this week</span></article><article><small>YOUR TURN</small><strong>3</strong><span>decisions waiting</span></article><article><small>AGENTS WORKING</small><strong>5</strong><span className="up">2 active now</span></article></div>
        <div className="preview-grid"><article className="work-panel"><header><div><small>OPERATING PULSE</small><h3>Work moving now</h3></div><span>View all →</span></header><div className="work-row"><i className="blue"/><div><b>Review Q3 campaign brief</b><small>Marketing · Assigned to Maya</small></div><em>In review</em></div><div className="work-row"><i className="green"/><div><b>Reconcile August operating expenses</b><small>Finance · Agent working</small></div><em>Running</em></div><div className="work-row"><i className="gold"/><div><b>Confirm vendor insurance certificate</b><small>Operations · Needs you</small></div><em>Waiting</em></div></article><article className="mia-panel"><header><span>M</span><div><small>OPERATING COORDINATOR</small><h3>Mia</h3></div><i /></header><p>Three decisions need your context today. I grouped the two vendor items so you can resolve them together.</p><button>Open your turn <span>→</span></button><footer><i /> Grounded in 148 business records</footer></article></div>
      </section>
    </div>
    <div className="preview-glow" />
  </div>;
}

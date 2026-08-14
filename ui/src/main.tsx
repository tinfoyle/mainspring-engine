import React, { FormEvent, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion } from "motion/react";
import {
  Activity, ArrowLeft, Bell, BookOpen, Bot, BriefcaseBusiness, Check, ChevronRight, Clock3,
  CircleDollarSign, ClipboardCheck, Command, FileText, Home, Inbox, LayoutDashboard,
  LoaderCircle, Menu, MessageSquareText, Paperclip, Plus, Search, Send, Settings,
  Sparkles, UploadCloud, Users, X,
} from "lucide-react";
import { api } from "./api";
import type { AppConfig, Approval, BaselinePayload, ConversationPayload, CoordinatorMessage, Document, EvidenceRequirement, FinanceEntry, FinancePayload, Message, TicketPayload, WorkItem, YourTurnPayload } from "./types";
import "./v2.css";

const rootElement = document.getElementById("mainspring-v2-root");
if (!rootElement) throw new Error("Mainspring V2 root is missing");

const config: AppConfig = {
  tenant: rootElement.dataset.tenant || "Mainspring",
  csrf: rootElement.dataset.csrf || "",
  user: {
    DisplayName: rootElement.dataset.user || "Owner",
    Email: rootElement.dataset.email || "",
    Role: rootElement.dataset.role || "member",
    Development: rootElement.dataset.development === "true",
  },
};

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 10_000, refetchOnWindowFocus: true, refetchOnReconnect: true, retry: 1 } },
});

const routeEvent = "mainspring:navigate";
function subscribeRoute(listener: () => void) {
  window.addEventListener("popstate", listener);
  window.addEventListener(routeEvent, listener);
  return () => { window.removeEventListener("popstate", listener); window.removeEventListener(routeEvent, listener); };
}
function routeSnapshot() { return `${window.location.pathname}${window.location.search}`; }
function navigate(to: string, options?: { replace?: boolean }) {
  options?.replace ? window.history.replaceState(null, "", to) : window.history.pushState(null, "", to);
  window.dispatchEvent(new Event(routeEvent));
}
function useRouterState() {
  const current = useSyncExternalStore(subscribeRoute, routeSnapshot, () => "/work");
  const url = new URL(current, window.location.origin);
  return { pathname: url.pathname, search: url.search };
}
function useSearchParamsV2(): [URLSearchParams, (next: URLSearchParams, options?: { replace?: boolean }) => void] {
  const { pathname, search } = useRouterState();
  return [new URLSearchParams(search), (next, options) => navigate(`${pathname}${next.toString() ? `?${next}` : ""}`, options)];
}
function Link({ to, children, className, onClick }: { to: string; children: React.ReactNode; className?: string; onClick?: () => void }) {
  return <a href={to} className={className} onClick={event => { if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); navigate(to); onClick?.(); }}>{children}</a>;
}

const nav = [
  { href: "/", label: "Home", icon: Home },
  { href: "/work", label: "Work", icon: BriefcaseBusiness },
  { href: "/baseline", label: "Baseline", icon: LayoutDashboard },
  { href: "/agents", label: "Agents", icon: Bot },
  { href: "/your-turn", label: "Your turn", icon: ClipboardCheck },
  { href: "/documents", label: "Documents", icon: FileText },
  { href: "/finance", label: "Finance", icon: CircleDollarSign },
  { href: "/inbox", label: "Inbox", icon: Inbox },
];

function AppShell() {
  const location = useRouterState();
  const [mobileNav, setMobileNav] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [noticesOpen, setNoticesOpen] = useState(false);
  const inbox = useQuery({queryKey:["inbox"],queryFn:api.inbox,staleTime:30_000});
  useEffect(() => { window.scrollTo({ top: 0, behavior: "auto" }); }, [location.pathname]);
  useEffect(() => {
    const title = location.pathname === "/" ? "Home"
      : location.pathname.startsWith("/work/") ? "Work item"
      : location.pathname === "/work" ? "Work"
      : location.pathname === "/your-turn" ? "Your turn"
      : location.pathname.startsWith("/documents/") ? "Document"
      : location.pathname === "/documents" ? "Documents"
      : location.pathname.startsWith("/boardrooms/") ? "Boardroom"
      : location.pathname.startsWith("/conversations/") ? "Conversation"
      : location.pathname === "/agents" ? "Agents"
      : location.pathname === "/finance" ? "Finance"
      : location.pathname === "/baseline" ? "Baseline"
      : location.pathname === "/inbox" ? "Inbox"
      : "Mainspring";
    document.title = `${title} · Mainspring`;
  }, [location.pathname]);
  useEffect(()=>{const handle=(event:KeyboardEvent)=>{if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==="k"){event.preventDefault();setCommandOpen(true);}if(event.key==="Escape"){setCommandOpen(false);setNoticesOpen(false);}};window.addEventListener("keydown",handle);return()=>window.removeEventListener("keydown",handle);},[]);
  const routeContent = location.pathname === "/"
    ? <HomeDashboard/>
    : location.pathname === "/work"
    ? <WorkQueue/>
    : location.pathname.startsWith("/work/")
      ? <Ticket id={decodeURIComponent(location.pathname.slice("/work/".length).split("/")[0] || "")}/>
      : location.pathname === "/your-turn"
        ? <YourTurn/>
      : location.pathname === "/documents"
        ? <Documents/>
      : location.pathname.startsWith("/documents/")
        ? <DocumentDetail id={decodeURIComponent(location.pathname.slice("/documents/".length).split("/")[0] || "")}/>
      : location.pathname.startsWith("/boardrooms/")
        ? <BoardroomRoom id={decodeURIComponent(location.pathname.slice("/boardrooms/".length).split("/")[0] || "")}/>
      : location.pathname.startsWith("/conversations/")
        ? <BoardroomConversation id={decodeURIComponent(location.pathname.slice("/conversations/".length).split("/")[0] || "")}/>
      : location.pathname === "/agents"
        ? <Agents/>
      : location.pathname === "/finance"
        ? <Finance/>
      : location.pathname === "/baseline"
        ? <BaselineWorkspace/>
      : location.pathname === "/inbox"
        ? <InboxPage/>
      : <ErrorState message="This V2 route is not available yet." retry={() => navigate("/work")}/>;
  return <div className="v2-app">
    <aside className={`v2-sidebar ${mobileNav ? "is-open" : ""}`}>
      <div className="v2-brand"><span className="v2-brand-mark">M</span><span><strong>Mainspring</strong><small>{config.tenant}</small></span></div>
      <nav className="v2-nav" aria-label="Primary navigation">
        <p>Workspace</p>
        {nav.map(({ href, label, icon: Icon }) => {
          const active = href === "/" ? location.pathname === "/" : href === "/work" ? location.pathname.startsWith("/work") : href === "/documents" ? location.pathname.startsWith("/documents") : location.pathname === href;
          const content = <><Icon size={18}/><span>{label}</span>{active && <motion.span layoutId="nav-active" className="v2-nav-active"/>}</>;
          return <Link key={href} to={href} className={active ? "is-active" : ""} onClick={() => setMobileNav(false)}>{content}</Link>;
        })}
      </nav>
      <div className="v2-sidebar-foot">
        <div className="v2-avatar">{initials(config.user.DisplayName)}</div>
        <span><strong>{config.user.DisplayName}</strong><small>{config.user.Role}</small></span>
        <a href="/agents" aria-label="Settings"><Settings size={17}/></a>
      </div>
    </aside>
    <div className="v2-stage">
      <header className="v2-topbar">
        <button className="v2-icon-button v2-menu-button" onClick={() => setMobileNav(!mobileNav)} aria-label="Toggle navigation"><Menu size={20}/></button>
        <div className="v2-live"><i/><span>Workspace live</span></div>
        <button className="v2-command" onClick={()=>{setNoticesOpen(false);setCommandOpen(true);}}><Search size={16}/><span>Search Mainspring</span><kbd><Command size={12}/> K</kbd></button>
        <button className="v2-icon-button" aria-label="Notifications" onClick={()=>{setCommandOpen(false);setNoticesOpen(value=>!value);}}><Bell size={19}/>{(inbox.data?.unread||0)>0&&<i className="v2-notification-dot"/>}</button>
      </header>
      <main className="v2-content">
        <AnimatePresence mode="wait">
          <motion.div key={location.pathname} initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -4 }} transition={{ duration: .18, ease: [0.2, 0.8, 0.2, 1] }}>
            {routeContent}
          </motion.div>
        </AnimatePresence>
      </main>
    </div>
    {mobileNav && <button className="v2-nav-scrim" aria-label="Close navigation" onClick={() => setMobileNav(false)}/>} 
    <AnimatePresence>{commandOpen&&<CommandPalette close={()=>setCommandOpen(false)}/>} {noticesOpen&&<NotificationTray data={inbox.data} close={()=>setNoticesOpen(false)}/>}</AnimatePresence>
  </div>;
}

function CommandPalette({close}:{close:()=>void}) { const [search,setSearch]=useState(""); const options=[...nav.map(item=>({title:item.label,description:`Open ${item.label}`,href:item.href,icon:item.icon})),{title:"Ask a boardroom",description:"Choose a team and start a coordinated conversation",href:"/",icon:MessageSquareText},{title:"Create work",description:"Add a ticket or to-do",href:"/work",icon:Plus},{title:"Upload a document",description:"Add evidence to business memory",href:"/documents",icon:UploadCloud}]; const filtered=options.filter(item=>`${item.title} ${item.description}`.toLowerCase().includes(search.toLowerCase())); return <motion.div className="v2-command-scrim" initial={{opacity:0}} animate={{opacity:1}} exit={{opacity:0}} onMouseDown={event=>event.target===event.currentTarget&&close()}><motion.section className="v2-command-palette" initial={{opacity:0,y:-14,scale:.985}} animate={{opacity:1,y:0,scale:1}} exit={{opacity:0,y:-8}}><header><Search size={19}/><input autoFocus value={search} onChange={event=>setSearch(event.target.value)} onKeyDown={event=>event.key==="Escape"&&close()} placeholder="Search pages and actions…"/><kbd>ESC</kbd></header><div>{filtered.map((item,index)=>{const Icon=item.icon;const content=<><span><Icon size={17}/></span><div><strong>{item.title}</strong><small>{item.description}</small></div><kbd>{index+1}</kbd></>;return <Link key={`${item.href}-${item.title}`} to={item.href} onClick={close}>{content}</Link>;})}{!filtered.length&&<div className="v2-command-empty">No destination matches “{search}”.</div>}</div><footer>Navigate Mainspring without leaving your flow.</footer></motion.section></motion.div>; }

function NotificationTray({data,close}:{data:Awaited<ReturnType<typeof api.inbox>>|undefined;close:()=>void}) { return <motion.aside className="v2-notification-tray" aria-label="Notifications" initial={{opacity:0,x:18}} animate={{opacity:1,x:0}} exit={{opacity:0,x:12}}><header><div><span>Updates</span><strong>{data?.unread||0} unread</strong></div><button aria-label="Close notifications" onClick={close}><X size={17}/></button></header><div>{(data?.items||[]).slice(0,5).map(item=><article key={item.ID} className={!item.Read?"is-unread":""}><span>{item.Category}</span><strong>{item.Title}</strong><p>{item.Body}</p><small>{item.PublishedAt}</small></article>)}{!data?.items.length&&<p className="v2-muted">No service updates.</p>}</div><Link to="/inbox" onClick={close}>Open inbox <ChevronRight size={15}/></Link></motion.aside>; }

function InboxPage() { const queryClient=useQueryClient(); const query=useQuery({queryKey:["inbox"],queryFn:api.inbox}); const mutation=useMutation({mutationFn:(id:string)=>api.readAnnouncement(config,id),onSuccess:next=>queryClient.setQueryData(["inbox"],next)}); const data=query.data; return <section className="v2-page v2-inbox-page"><PageHeading eyebrow="Mainspring updates" title="Inbox" description="Important service notices and platform updates, presented without pulling you away from your work."/>{data&&<div className="v2-inbox-summary"><Bell size={19}/><strong>{data.unread}</strong><span>unread updates</span></div>}{query.isLoading&&<WorkSkeleton/>}{query.isError&&<ErrorState message={query.error.message} retry={()=>query.refetch()}/>} {data&&<motion.div className="v2-inbox-list" layout>{data.items.map((item,index)=><motion.article key={item.ID} className={!item.Read?"is-unread":""} layout initial={{opacity:0,y:8}} animate={{opacity:1,y:0}} transition={{delay:index*.03}}><i/><header><span>{item.Category}</span><time>{item.PublishedAt}</time></header><h2>{item.Title}</h2><p>{item.Body}</p>{!item.Read&&<button disabled={mutation.isPending} onClick={()=>mutation.mutate(item.ID)}><Check size={15}/> Mark read</button>}</motion.article>)}{!data.items.length&&<EmptyState title="You’re all caught up" body="Service updates will appear here when there is something worth your attention."/>}</motion.div>}</section>; }

function HomeDashboard() {
  const query = useQuery({ queryKey: ["home"], queryFn: api.home, refetchInterval: 10_000, refetchIntervalInBackground: false });
  const data = query.data;
  const attention = data ? data.yourTurn.Inputs + data.yourTurn.Reviews + data.yourTurn.Approvals : 0;
  return <section className="v2-page v2-home">
    <header className="v2-home-hero"><div><span>Your business workspace</span><h1>Good to see you, {config.user.DisplayName.split(" ")[0]}.</h1><p>Mainspring keeps your business evidence, agent work, and owner decisions moving together.</p></div><div className="v2-home-orbit"><span>M</span><i/><i/><i/></div></header>
    {query.isLoading && <div className="v2-metrics">{[1,2,3,4].map(item => <div className="v2-skeleton work" key={item}/>)}</div>}
    {query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {data && <><div className="v2-home-pulse"><Link to="/work"><BriefcaseBusiness size={19}/><div><strong>{data.work.Active + data.work.InProgress}</strong><span>pieces of work moving</span></div><ChevronRight size={17}/></Link><Link to="/your-turn"><ClipboardCheck size={19}/><div><strong>{attention}</strong><span>items need your attention</span></div><ChevronRight size={17}/></Link><Link to="/documents"><FileText size={19}/><div><strong>{data.documents}</strong><span>records in business memory</span></div><ChevronRight size={17}/></Link></div><section className="v2-home-section"><header><div><span>Ask the team</span><h2>Your boardrooms</h2><p>Bring a question to a coordinated group of agents grounded in your business.</p></div></header><div className="v2-room-grid">{data.boardrooms.map((room, index) => <motion.article key={room.ID} initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: index * .04 }} onClick={() => navigate(`/boardrooms/${room.ID}`)} tabIndex={0} onKeyDown={event => event.key === "Enter" && navigate(`/boardrooms/${room.ID}`)}><div><span>{initials(room.Name)}</span><StatusBadge status={room.Status}/></div><h3>{room.Name}</h3><p>{room.Description}</p><footer><span><Users size={14}/>{room.PersonaCount} agents</span><strong>Open room <ChevronRight size={15}/></strong></footer></motion.article>)}</div></section><section className="v2-home-section"><header><div><span>Everything connected</span><h2>Your Mainspring toolkit</h2><p>Move between outcomes without losing the business context behind them.</p></div></header><div className="v2-tool-grid"><ToolCard to="/baseline" icon={LayoutDashboard} title="Baseline" body="Build the documented source of truth for your business." legacy/><ToolCard to="/work" icon={BriefcaseBusiness} title="Work" body="See what agents, owners, and outside parties are moving."/><ToolCard to="/documents" icon={FileText} title="Documents" body="Give agents trustworthy records they can retrieve and cite."/><ToolCard to="/finance" icon={CircleDollarSign} title="Finance" body="Maintain ledgers, accounts, income, and expenses." legacy/><ToolCard to="/your-turn" icon={ClipboardCheck} title="Your turn" body="Handle the few questions and decisions only you can answer."/><ToolCard to="/agents" icon={Bot} title="Agents" body="Understand and tune the specialists working for you."/></div></section></>}
  </section>;
}

function ToolCard({ to, icon: Icon, title, body, legacy }: { to: string; icon: typeof Home; title: string; body: string; legacy?: boolean }) {
  const content = <><span><Icon size={19}/></span><div><strong>{title}</strong><p>{body}</p></div><ChevronRight size={17}/></>;
  return legacy ? <a className="v2-tool-card" href={to}>{content}</a> : <Link className="v2-tool-card" to={to}>{content}</Link>;
}

function Agents() {
  const query = useQuery({ queryKey: ["agents"], queryFn: api.agents });
  const data = query.data;
  const active = data?.agents.filter(agent => agent.Enabled).length || 0;
  return <section className="v2-page v2-agents-page"><PageHeading eyebrow="Specialist team" title="Agents" description="The people behind the work: their purpose, boardroom, capabilities, and operating status." actions={<a className="v2-button primary" href="/agents/new"><Plus size={17}/> Create agent</a>}/>{data && <div className="v2-agent-summary"><div><strong>{active}</strong><span>active agents</span></div><div><strong>{new Set(data.agents.map(agent => agent.BoardroomName)).size}</strong><span>boardrooms staffed</span></div><div><strong>{data.agents.reduce((sum, agent) => sum + agent.ToolCount, 0)}</strong><span>tool permissions</span></div></div>}{query.isLoading && <div className="v2-agent-grid">{[1,2,3,4,5,6].map(item => <div className="v2-skeleton document" key={item}/>)}</div>}{query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} {data && <motion.div className="v2-agent-grid" layout>{data.agents.map((agent, index) => <motion.a href={`/agents/${agent.ID}?legacy=1`} key={agent.ID} className={!agent.Enabled ? "is-disabled" : ""} initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: Math.min(index * .025, .18) }}><header><span>{initials(agent.Name)}</span><div><h2>{agent.Name}</h2><p>{agent.Role}</p></div><i className={agent.Enabled ? "active" : ""}>{agent.Enabled ? "Active" : "Inactive"}</i></header><p>{agent.Description || "No purpose has been described yet."}</p><dl><div><dt>Boardroom</dt><dd>{agent.BoardroomName}</dd></div><div><dt>Position</dt><dd>{agent.Position}</dd></div><div><dt>Tools</dt><dd>{agent.ToolCount}</dd></div><div><dt>Model</dt><dd>{agent.Model || (agent.Provider === "inherit" ? "Default" : agent.Provider)}</dd></div></dl><footer><span>{agent.MaxTurns} turn room cap</span><strong>Configure <ChevronRight size={14}/></strong></footer></motion.a>)}</motion.div>}
  </section>;
}

const baselineSteps = [{key:"interview",label:"Interview"},{key:"inventory",label:"Evidence"},{key:"gap_review",label:"Gaps"},{key:"plan_approval",label:"Plan"},{key:"active",label:"Welcome"}];
function baselineStepIndex(phase: string) { return ({interview:0,source_access:1,inventory:1,gap_review:2,plan_approval:3,baseline_active:4,baseline_ready:4} as Record<string,number>)[phase] ?? 4; }

function BaselineWorkspace() {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: ["baseline"], queryFn: api.baseline });
  const data = query.data;
  const setData = (next: BaselinePayload) => { queryClient.setQueryData(["baseline"], next); queryClient.invalidateQueries({queryKey:["home"]}); };
  const phase = data?.baseline.Phase || "interview";
  return <section className="v2-page v2-baseline"><PageHeading eyebrow="Documented business baseline" title="Teach Mainspring how the business really works" description="Interview, collect, verify, assign, and reassess. Every conclusion remains tied to its source."/>
    <nav className="v2-baseline-progress" aria-label="Baseline progress">{baselineSteps.map((step,index) => <div key={step.key} className={index === baselineStepIndex(phase) ? "is-current" : index < baselineStepIndex(phase) ? "is-complete" : ""}><span>{index < baselineStepIndex(phase) ? <Check size={14}/> : index + 1}</span><strong>{step.label}</strong><i/></div>)}</nav>
    {query.isLoading && <div className="v2-skeleton conversation"/>}{query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {data && phase === "interview" && <BaselineInterviewView data={data} update={setData}/>} {data && phase === "inventory" && <BaselineEvidenceInterview data={data} update={setData}/>} {data && phase === "gap_review" && <BaselineGapReview data={data} update={setData}/>} {data && phase === "plan_approval" && <BaselinePlan data={data} update={setData}/>} {data && !["interview","inventory","gap_review","plan_approval"].includes(phase) && <BaselineComplete data={data} update={setData}/>} 
  </section>;
}

function BaselineInterviewView({data,update}:{data:BaselinePayload;update:(next:BaselinePayload)=>void}) {
  const baseline=data.baseline; const [answer,setAnswer]=useState(""); const transcript=useRef<HTMLDivElement>(null);
  const mutation=useMutation({mutationFn:()=>api.baselineMutation(config,"/interview",{answer:answer.trim()}),onSuccess:next=>{setAnswer("");update(next);}});
  useEffect(()=>{const node=transcript.current;if(node)requestAnimationFrame(()=>node.scrollTo({top:node.scrollHeight,behavior:"smooth"}));},[baseline.Messages.length]);
  return <div className="v2-baseline-grid"><section className="v2-mia-panel"><header className="v2-mia-header"><div className="v2-mia-identity"><span>M</span><div><small>Setup manager</small><h2>Interview with Mia</h2><p>Start with what you know. Mia will shape the evidence plan around the business.</p></div></div><div className="v2-baseline-count"><strong>{baseline.CurrentQuestion+1}</strong><span>of {baseline.QuestionCount}</span></div></header><div className="v2-mia-transcript" ref={transcript}>{baseline.Messages.map((message,index)=><CoordinatorBubble key={index} message={{Role:message.Role,MessageKind:"",Body:message.Body,CreatedAt:""}}/>)}{baseline.CurrentPrompt && <motion.article className="v2-coordinator-message" initial={{opacity:0,y:8}} animate={{opacity:1,y:0}}><div><strong>Mia</strong></div><p>{baseline.CurrentPrompt}</p></motion.article>}</div><form className="v2-mia-compose" onSubmit={event=>{event.preventDefault();if(answer.trim())mutation.mutate();}}><textarea autoFocus value={answer} onChange={event=>setAnswer(event.target.value)} onKeyDown={event=>{if(event.key==="Enter"&&!event.shiftKey&&!event.nativeEvent.isComposing&&answer.trim()){event.preventDefault();mutation.mutate();}}} placeholder="Answer naturally, or say you’re not sure yet."/><footer><span className="v2-composer-hint">{baseline.CurrentExplanation}</span><button className="v2-send" disabled={!answer.trim()||mutation.isPending}>{mutation.isPending?<LoaderCircle className="spin" size={17}/>:<Send size={17}/>} Answer and continue</button></footer>{mutation.isError&&<div className="v2-error-inline">{mutation.error.message}</div>}</form></section><BaselineFacts facts={baseline.Facts}/></div>;
}

function BaselineFacts({facts}:{facts:BaselinePayload["baseline"]["Facts"]}) { return <aside className="v2-side-card v2-baseline-facts"><div className="v2-side-heading"><span>What Mainspring knows</span><BookOpen size={17}/></div><p className="v2-muted">Confirmed facts become shared context for every agent.</p><div>{facts.map(fact=><article key={fact.Key}><span>{fact.Label}</span><strong>{fact.Value}</strong><small>{fact.SourceLabel}</small></article>)}{!facts.length&&<p className="v2-muted">Your answers will appear here as reusable business facts.</p>}</div></aside>; }

function BaselineEvidenceInterview({data,update}:{data:BaselinePayload;update:(next:BaselinePayload)=>void}) {
  const baseline=data.baseline,current=baseline.InventoryCurrent; const [answer,setAnswer]=useState(""); const [documentID,setDocumentID]=useState(""); const transcript=useRef<HTMLDivElement>(null);
  const mutation=useMutation({mutationFn:(choice?:string)=>api.baselineMutation(config,`/evidence/${current!.ID}/interview`,{answer:answer.trim()||evidenceChoiceAnswer(choice||""),choice:choice||""}),onSuccess:next=>{setAnswer("");setDocumentID("");update(next);}});
  const linkMutation=useMutation({mutationFn:()=>api.baselineMutation(config,`/evidence/${current!.ID}/documents`,{document_id:documentID}),onSuccess:next=>{setDocumentID("");update(next);}});
  const advance=useMutation({mutationFn:()=>api.baselineMutation(config,"/advance",{phase:"gap_review"}),onSuccess:update});
  useEffect(()=>{const node=transcript.current;if(node)requestAnimationFrame(()=>node.scrollTo({top:node.scrollHeight,behavior:"smooth"}));},[baseline.InventoryAnswered,current?.ID]);
  const recent=baseline.Requirements.filter(requirement=>requirement.Interviewed).slice(-3);
  return <section className="v2-evidence-interview"><header><div><span>Evidence interview with Mia</span><h2>Let’s work through this together</h2><p>One relevant topic at a time—no mile-long form.</p></div><strong>{baseline.InventoryAnswered} <small>of {baseline.InventoryTotal}</small></strong></header><div className="v2-scope-note"><Sparkles size={19}/><div><strong>Scoped for a {baseline.EvidenceScopeTitle}</strong><p>{baseline.EvidenceScopeSummary}</p></div></div><div className="v2-progress-track"><motion.i animate={{width:`${baseline.InventoryTotal?baseline.InventoryAnswered*100/baseline.InventoryTotal:0}%`}}/></div><div className="v2-evidence-transcript" ref={transcript}>{recent.map(requirement=><React.Fragment key={requirement.ID}><EvidenceChatBubble requirement={requirement} ask/><motion.article className="v2-coordinator-message is-user"><div><strong>You</strong></div><p>{requirement.OwnerAnswer}</p></motion.article><EvidenceChatBubble requirement={requirement}/></React.Fragment>)}{current?<EvidenceChatBubble requirement={current} ask/>:<motion.article className="v2-coordinator-message"><div><strong>Mia</strong></div><p>That covers every relevant baseline topic. I’ve translated your answers into a proposed evidence and work plan.</p></motion.article>}</div>{current?<div className="v2-evidence-compose"><div className="v2-current-evidence"><span>{current.Domain}</span><h3>{current.Label}</h3><p>{current.Rationale}</p>{current.Evidence.length>0&&<div><strong>{current.Evidence.length} possible record(s) found</strong>{current.Evidence.map(link=><a key={link.Label} href={link.URL||undefined}>{link.Label}</a>)}</div>}{current.Research.length>0&&<details><summary><Search size={14}/> See what Mia searched</summary>{current.Research.map((run,index)=><div key={index}><code>{run.Query}</code>{run.Results.map(result=><a key={result.URL} href={result.URL} target="_blank" rel="noreferrer">{result.Title}<small>{result.Description}</small></a>)}</div>)}</details>}</div><div className="v2-quick-replies">{[["have_it","I have it"],["search_sources","Please find it"],["create_it","We need to create it"],["obtain_it","We need to obtain it"],["not_applicable","Doesn’t apply"]].map(([value,label])=><button key={value} disabled={mutation.isPending} onClick={()=>mutation.mutate(value)}>{label}</button>)}</div>{baseline.Documents.length>0&&<div className="v2-link-document"><select value={documentID} onChange={event=>setDocumentID(event.target.value)}><option value="">Attach an uploaded document…</option>{baseline.Documents.map(document=><option key={document.ID} value={document.ID}>{document.Name}</option>)}</select><button disabled={!documentID||linkMutation.isPending} onClick={()=>linkMutation.mutate()}>Use document</button></div>}<form onSubmit={event=>{event.preventDefault();if(answer.trim())mutation.mutate(undefined);}}><textarea autoFocus value={answer} onChange={event=>setAnswer(event.target.value)} onKeyDown={event=>{if(event.key==="Enter"&&!event.shiftKey&&!event.nativeEvent.isComposing&&answer.trim()){event.preventDefault();mutation.mutate(undefined);}}} placeholder="Tell Mia what you know…"/><footer><Link to="/documents"><Paperclip size={14}/> Add a record</Link><span>Enter sends · Shift + Enter adds a line</span><button className="v2-send" disabled={!answer.trim()||mutation.isPending}><Send size={16}/> Send</button></footer></form>{(mutation.isError||linkMutation.isError)&&<div className="v2-error-inline">{mutation.error?.message||linkMutation.error?.message}</div>}</div>:<div className="v2-evidence-complete"><Check size={22}/><div><strong>Interview complete</strong><p>All {baseline.InventoryTotal} topics have an answer or verified record.</p></div><button className="v2-button primary" disabled={advance.isPending} onClick={()=>advance.mutate()}>Review Mia’s proposed plan <ChevronRight size={16}/></button></div>}</section>;
}

function EvidenceChatBubble({requirement,ask}:{requirement:EvidenceRequirement;ask?:boolean}) { return <motion.article className="v2-coordinator-message" initial={{opacity:0,y:7}} animate={{opacity:1,y:0}}><div><strong>Mia</strong><span>{requirement.Domain}</span></div><p>{ask?`Next, let’s cover ${requirement.Label}. ${requirement.Rationale} Do you already have this, should I help find or create it, or does it not apply?`:evidenceAcknowledgement(requirement)}</p></motion.article>; }
function evidenceChoiceAnswer(choice:string){return ({have_it:"I have this, but I need to locate or upload it later.",search_sources:"Please search the available sources for this.",create_it:"We do not have this yet; Mainspring should help create it.",obtain_it:"We need to obtain this from an outside provider or authority.",not_applicable:"This does not apply to our business."} as Record<string,string>)[choice]||"";}
function evidenceAcknowledgement(requirement:EvidenceRequirement){if(requirement.Status==="confirmed")return"Got it. I’ll treat that as verified evidence and keep its source attached.";return ({have_it:"I’ll add a follow-up to locate and verify the record.",search_sources:"I’ll make this an agent research item and preserve what was searched.",create_it:"I’ll turn this into agent work to draft the missing record.",obtain_it:"I’ll track this as something the business needs to obtain.",not_applicable:"I’ll mark this not applicable so it creates no unnecessary work."}as Record<string,string>)[requirement.Disposition]||"I’ve recorded that and will carry it into the review.";}

function BaselineGapReview({data,update}:{data:BaselinePayload;update:(next:BaselinePayload)=>void}) { const advance=useMutation({mutationFn:()=>api.baselineMutation(config,"/advance",{phase:"plan_approval"}),onSuccess:update}); return <section className="v2-gap-review"><header><span>What Mia heard</span><h2>Review the documented gaps</h2><p>You already made these decisions in the interview. This is the clear recap—not another form.</p></header><div>{data.baseline.Requirements.map(requirement=><article key={requirement.ID}><header><div><span>{requirement.Domain}</span><h3>{requirement.Label}</h3></div><StatusBadge status={requirement.Status}/></header><div><section><span>You told Mia</span><blockquote>{requirement.OwnerAnswer||"No additional explanation was recorded."}</blockquote></section><section><span>Recorded outcome</span><strong>{evidenceOutcome(requirement)}</strong><small>{responsibilityLabel(requirement.Responsibility)}</small></section></div>{requirement.Evidence.length>0&&<footer><Paperclip size={14}/>{requirement.Evidence.map(link=><a key={link.Label} href={link.URL}>{link.Label}</a>)}</footer>}</article>)}</div><button className="v2-button primary" disabled={advance.isPending} onClick={()=>advance.mutate()}>Review the baseline plan <ChevronRight size={16}/></button></section>; }
function evidenceOutcome(requirement:EvidenceRequirement){if(requirement.Status==="confirmed")return"Verified evidence is attached";return ({have_it:"Locate and verify the existing record",search_sources:"Mia will search the available sources",create_it:"Mainspring will draft the missing record",obtain_it:"The business will obtain it from an outside source",not_applicable:"Not applicable to this business"}as Record<string,string>)[requirement.Disposition]||"Follow-up is still required";}

function BaselinePlan({data,update}:{data:BaselinePayload;update:(next:BaselinePayload)=>void}) { const mutation=useMutation({mutationFn:()=>api.baselineMutation(config,"/plan",{approve:"true"}),onSuccess:update}); const counts=data.baseline.Requirements.filter(item=>!["confirmed","not_applicable"].includes(item.Status)).reduce((all,item)=>({...all,[item.Responsibility]:(all[item.Responsibility]||0)+1}),{}as Record<string,number>); return <section className="v2-baseline-plan"><div className="v2-baseline-plan-icon"><ClipboardCheck size={31}/></div><span>Owner approval</span><h2>Create the Business Baseline Plan</h2><p>Mainspring will create one parent ticket and a child ticket for each unresolved evidence requirement. This does not submit applications, make payments, send email, or authorize external actions.</p><div>{[["agent","agent tasks"],["owner","owner tasks"],["shared","shared tasks"],["external","external dependencies"]].map(([key,label])=><section key={key}><strong>{counts[key]||0}</strong><span>{label}</span></section>)}</div>{mutation.isError&&<div className="v2-error-inline">{mutation.error.message}</div>}<button className="v2-button primary" disabled={mutation.isPending} onClick={()=>mutation.mutate()}>{mutation.isPending?<LoaderCircle className="spin" size={17}/>:<Check size={17}/>} Approve and create work</button></section>; }

function BaselineComplete({data,update}:{data:BaselinePayload;update:(next:BaselinePayload)=>void}) { const ready=data.baseline.Phase==="baseline_ready"; const mutation=useMutation({mutationFn:()=>api.baselineMutation(config,"/reassess",{}),onSuccess:update}); return <section className="v2-baseline-complete"><div><Check size={32}/></div><span>{ready?"Baseline ready":"Onboarding complete"}</span><h2>{ready?"The business has a documented baseline":"Your Mainspring workspace is ready"}</h2><p>{ready?"Every required item has been confirmed or marked not applicable. Mainspring will help keep it current.":"Your approved plan is now in Work and can progress in the background."}</p><footer><Link className="v2-button primary" to="/">Go to home</Link><Link className="v2-button secondary" to="/work">View work</Link>{ready&&<button className="v2-button secondary" disabled={mutation.isPending} onClick={()=>mutation.mutate()}>Start reassessment</button>}</footer>{data.baseline.NextReassessment&&<small>Next reassessment: {data.baseline.NextReassessment}</small>}</section>; }

function Finance() {
  const [params, setParams] = useSearchParamsV2();
  const ledgerID = params.get("ledger") || "";
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: ["finance", ledgerID], queryFn: () => api.finance(ledgerID || undefined) });
  const [modal, setModal] = useState<"ledger" | "account" | "entry" | null>(null);
  const [entry, setEntry] = useState<FinanceEntry | null>(null);
  const data = query.data;
  useEffect(() => {
    if (!ledgerID && data?.selected?.ID) { const next = new URLSearchParams(params); next.set("ledger", data.selected.ID); setParams(next, { replace: true }); }
  }, [ledgerID, data?.selected?.ID]);
  const update = (next: FinancePayload) => {
    const nextID = next.selected?.ID || "";
    queryClient.setQueryData(["finance", nextID], next);
    queryClient.setQueryData(["finance", ledgerID], next);
    if (nextID && nextID !== ledgerID) { const search = new URLSearchParams(); search.set("ledger", nextID); setParams(search); }
    setModal(null); setEntry(null);
  };
  return <section className="v2-page v2-finance-page"><PageHeading eyebrow="Financial records" title="Finance" description="Balanced books that you and your agents can maintain together, with every change attributable and reversible." actions={<button className="v2-button primary" onClick={() => setModal("ledger")}><Plus size={17}/> New ledger</button>}/>{query.isLoading && <TicketSkeleton/>}{query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} {data && <div className="v2-finance-layout"><aside className="v2-finance-books"><header><span>Books</span><em>{data.ledgers.length}</em></header><nav>{data.ledgers.map(ledger => <button key={ledger.ID} className={data.selected?.ID === ledger.ID ? "is-active" : ""} onClick={() => { const next = new URLSearchParams(); next.set("ledger", ledger.ID); setParams(next); }}><strong>{ledger.Name}</strong><small>{ledger.Code} · {ledger.Currency}</small></button>)}</nav><button onClick={() => setModal("ledger")}><Plus size={15}/> Add ledger</button></aside><main className="v2-finance-main">{!data.selected ? <EmptyState title="Create your first ledger" body="Start with a standard chart, then let agents add accounts and record transactions as the business needs them."/> : <><section className="v2-finance-overview"><header><div><span>{data.selected.Code}</span><h2>{data.selected.Name}</h2><p>{data.selected.Description || "Shared financial book"}</p></div><StatusBadge status={data.selected.Status}/></header><div><div><span>Income</span><strong>{data.selected.Income}</strong></div><div><span>Expenses</span><strong>{data.selected.Expenses}</strong></div><div><span>Net</span><strong>{data.selected.Net}</strong></div><div><span>Drafts</span><strong>{data.selected.DraftCount}</strong></div></div></section><div className="v2-finance-columns"><section className="v2-finance-panel"><header><div><span>Chart of accounts</span><h2>Accounts and sub-accounts</h2></div><button onClick={() => setModal("account")}><Plus size={15}/> Add</button></header><div className="v2-account-list">{data.accounts.map(account => <div key={account.ID}><span className={`type-${account.Type}`}>{account.Code}</span><p><strong>{account.Name}</strong><small>{account.Type}{account.ParentAccountID ? " · sub-account" : ""}</small></p><b>{account.Balance}</b></div>)}</div></section><section className="v2-finance-panel v2-entry-prompt"><header><div><span>Double-entry journal</span><h2>Record a transaction</h2></div></header><div><CircleDollarSign size={30}/><p>Record income, expenses, transfers, and adjustments as balanced drafts before posting them.</p><button className="v2-button primary" onClick={() => setModal("entry")}><Plus size={16}/> New journal entry</button></div></section></div><section className="v2-finance-panel v2-journal"><header><div><span>Journal</span><h2>Recent entries</h2></div><em>{data.entries.length}</em></header><div>{data.entries.map(item => <button key={item.ID} onClick={() => setEntry(item)}><span><strong>#{item.Number} · {item.Description}</strong><small>{item.Date} · {item.Source}{item.Reference ? ` · ${item.Reference}` : ""}</small></span><span><b>{item.Total}</b><i className={item.Status}>{item.Status}</i></span></button>)}{!data.entries.length && <p className="v2-muted">No entries yet. Record the first balanced transaction, or ask an agent to do it.</p>}</div></section></>}</main></div>}<AnimatePresence>{modal === "ledger" && <LedgerModal close={() => setModal(null)} update={update}/>} {modal === "account" && data?.selected && <AccountModal data={data} close={() => setModal(null)} update={update}/>} {modal === "entry" && data?.selected && <EntryModal data={data} close={() => setModal(null)} created={async result => { setModal(null); const next = await api.finance(result.ledger.ID); update(next); setEntry(result.entry); }}/>} {entry && <EntryDetailModal entry={entry} close={() => setEntry(null)} update={update}/>}</AnimatePresence></section>;
}

function LedgerModal({ close, update }: { close: () => void; update: (next: FinancePayload) => void }) {
  const mutation = useMutation({ mutationFn: (form: FormData) => api.createLedger(config, form), onSuccess: update });
  return <motion.div className="v2-modal-scrim" initial={{opacity:0}} animate={{opacity:1}} exit={{opacity:0}} onMouseDown={event => event.target === event.currentTarget && close()}><motion.form className="v2-modal" initial={{opacity:0,y:18}} animate={{opacity:1,y:0}} onSubmit={event => {event.preventDefault(); mutation.mutate(new FormData(event.currentTarget));}}><header><div><span>Financial book</span><h2>Create a ledger</h2></div><button type="button" onClick={close}><X size={18}/></button></header><label>Name<input autoFocus required name="name" placeholder="Operating books"/></label><div className="v2-form-grid"><label>Code<input required name="code" placeholder="MAIN"/></label><label>Currency<input required name="currency" defaultValue="USD" maxLength={3}/></label></div><label>Description<textarea name="description" placeholder="Primary company ledger"/></label><label className="v2-check"><input type="checkbox" name="standard_accounts" value="yes" defaultChecked/><span>Create a standard chart of accounts</span></label>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}<footer><button type="button" className="v2-button secondary" onClick={close}>Cancel</button><button className="v2-button primary" disabled={mutation.isPending}>{mutation.isPending && <LoaderCircle className="spin" size={16}/>} Create ledger</button></footer></motion.form></motion.div>;
}

function AccountModal({ data, close, update }: { data: FinancePayload; close: () => void; update: (next: FinancePayload) => void }) {
  const ledger = data.selected!;
  const mutation = useMutation({ mutationFn: (form: FormData) => api.createAccount(config, ledger.ID, form), onSuccess: update });
  return <motion.div className="v2-modal-scrim" initial={{opacity:0}} animate={{opacity:1}} exit={{opacity:0}}><motion.form className="v2-modal" onSubmit={event => {event.preventDefault(); mutation.mutate(new FormData(event.currentTarget));}}><header><div><span>{ledger.Name}</span><h2>Add an account</h2></div><button type="button" onClick={close}><X size={18}/></button></header><div className="v2-form-grid"><label>Code<input autoFocus required name="code"/></label><label>Name<input required name="name"/></label></div><div className="v2-form-grid"><label>Type<select name="account_type"><option value="asset">Asset</option><option value="liability">Liability</option><option value="equity">Equity</option><option value="income">Income</option><option value="expense">Expense</option></select></label><label>Parent<select name="parent_account_id"><option value="">Top level</option>{data.accounts.map(account => <option key={account.ID} value={account.ID}>{account.Code} · {account.Name}</option>)}</select></label></div><label>Description<input name="description"/></label><label className="v2-check"><input type="checkbox" name="allow_posting" value="yes" defaultChecked/><span>Allow journal postings</span></label>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}<footer><button type="button" className="v2-button secondary" onClick={close}>Cancel</button><button className="v2-button primary" disabled={mutation.isPending}>Add account</button></footer></motion.form></motion.div>;
}

function EntryModal({ data, close, created }: { data: FinancePayload; close: () => void; created: (result: Awaited<ReturnType<typeof api.createEntry>>) => void }) {
  const ledger = data.selected!;
  const mutation = useMutation({ mutationFn: (form: FormData) => api.createEntry(config, ledger.ID, form), onSuccess: created });
  const accounts = data.accounts.filter(account => account.AllowPosting && account.Status === "active");
  return <motion.div className="v2-modal-scrim" initial={{opacity:0}} animate={{opacity:1}} exit={{opacity:0}}><motion.form className="v2-modal v2-entry-modal" onSubmit={event => {event.preventDefault(); mutation.mutate(new FormData(event.currentTarget));}}><header><div><span>{ledger.Name}</span><h2>New journal entry</h2></div><button type="button" onClick={close}><X size={18}/></button></header><div className="v2-form-grid"><label>Date<input type="date" name="entry_date" required defaultValue={data.today}/></label><label>Reference<input name="reference" placeholder="Invoice or receipt #"/></label></div><label>Description<input autoFocus name="description" required placeholder="What happened?"/></label><div className="v2-entry-lines"><header><span>Account</span><span>Memo</span><span>Debit</span><span>Credit</span></header>{[1,2,3,4].map(index => <div key={index}><select name={`line_${index}_account`} required={index < 3}><option value="">Select account</option>{accounts.map(account => <option key={account.ID} value={account.ID}>{account.Code} · {account.Name}</option>)}</select><input name={`line_${index}_memo`}/><input inputMode="decimal" name={`line_${index}_debit`} placeholder="0.00"/><input inputMode="decimal" name={`line_${index}_credit`} placeholder="0.00"/></div>)}</div>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}<footer><button type="button" className="v2-button secondary" onClick={close}>Cancel</button><button className="v2-button primary" disabled={mutation.isPending}>Save balanced draft</button></footer></motion.form></motion.div>;
}

function EntryDetailModal({ entry, close, update }: { entry: FinanceEntry; close: () => void; update: (next: FinancePayload) => void }) {
  const mutation = useMutation({ mutationFn: (action: "post" | "void") => api.financeEntryAction(config, entry.ID, action), onSuccess: update });
  return <motion.div className="v2-modal-scrim" initial={{opacity:0}} animate={{opacity:1}} exit={{opacity:0}} onMouseDown={event => event.target === event.currentTarget && close()}><motion.section className="v2-modal v2-entry-detail"><header><div><span>Journal entry #{entry.Number}</span><h2>{entry.Description}</h2></div><button onClick={close}><X size={18}/></button></header><div className="v2-entry-total"><strong>{entry.Total}</strong><StatusBadge status={entry.Status}/></div><div className="v2-entry-detail-lines"><header><span>Account</span><span>Memo</span><span>Debit</span><span>Credit</span></header>{entry.Lines.map((line,index) => <div key={`${line.AccountID}-${index}`}><strong>{line.AccountCode} · {line.AccountName}</strong><span>{line.Memo || "—"}</span><span>{line.Debit || "—"}</span><span>{line.Credit || "—"}</span></div>)}</div>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}<footer><button className="v2-button secondary" onClick={close}>Close</button>{entry.Status === "draft" && <button className="v2-button primary" disabled={mutation.isPending} onClick={() => mutation.mutate("post")}>Post entry</button>}{entry.Status === "posted" && !entry.ReversalOfID && <button className="v2-button danger" disabled={mutation.isPending} onClick={() => mutation.mutate("void")}>Void with reversal</button>}</footer></motion.section></motion.div>;
}

function WorkQueue() {
  const [params, setParams] = useSearchParamsV2();
  const query = useQuery({ queryKey: ["work", params.toString()], queryFn: () => api.workQueue(params.toString() ? `?${params}` : ""), refetchInterval: 5_000, refetchIntervalInBackground: false });
  const [creating, setCreating] = useState(false);
  const [search, setSearch] = useState(params.get("q") || "");

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      const next = new URLSearchParams(params);
      search ? next.set("q", search) : next.delete("q");
      if (next.toString() !== params.toString()) setParams(next, { replace: true });
    }, 250);
    return () => window.clearTimeout(timeout);
  }, [search]);

  const data = query.data;
  const parents = useMemo(() => data?.items.filter(item => !item.ParentID) || [], [data]);
  const children = useMemo(() => (data?.items.filter(item => item.ParentID) || []).reduce((groups, item) => {
    const group = groups.get(item.ParentID) || [];
    group.push(item);
    groups.set(item.ParentID, group);
    return groups;
  }, new Map<string, WorkItem[]>()), [data]);
  return <section className="v2-page">
    <PageHeading eyebrow="Operations center" title="Work" description="A live view of what your company and its agents are moving forward." actions={<button className="v2-button primary" onClick={() => setCreating(true)}><Plus size={17}/> New work</button>}/>
    {data && <div className="v2-metrics">
      <Metric label="Active" value={data.summary.Active} tone="green"/><Metric label="In progress" value={data.summary.InProgress} tone="blue"/><Metric label="Waiting on you" value={data.summary.Waiting} tone="amber"/><Metric label="Completed" value={data.summary.Done} tone="neutral"/>
    </div>}
    <div className="v2-work-toolbar">
      <div className="v2-tabs">{["active", "in_progress", "waiting", "done", "all"].map(status => <button key={status} className={(params.get("status") || "active") === status ? "is-active" : ""} onClick={() => { const next = new URLSearchParams(params); next.set("status", status); setParams(next); }}>{statusLabel(status)}</button>)}</div>
      <label className="v2-search"><Search size={17}/><input value={search} onChange={e => setSearch(e.target.value)} placeholder="Search work…"/></label>
    </div>
    {query.isLoading && <WorkSkeleton/>}
    {query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {data && <motion.div className="v2-work-list" layout>
      {parents.map((item, index) => <WorkCard key={item.ID} item={item} subtasks={children.get(item.ID) || []} index={index} open={() => navigate(`/work/${item.ID}`)}/>) }
      {!parents.length && <EmptyState title="No work matches this view" body="Try another filter, or create the next piece of work."/>}
    </motion.div>}
    <AnimatePresence>{creating && <CreateWorkModal close={() => setCreating(false)} onCreated={item => { setCreating(false); navigate(`/work/${item.ID}`); }}/>}</AnimatePresence>
  </section>;
}

function Ticket({ id }: { id: string }) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["ticket", id],
    queryFn: () => api.ticket(id),
    refetchInterval: current => runActive((current.state.data as TicketPayload | undefined)?.run.Status || "") ? 2_000 : false,
    refetchIntervalInBackground: false,
  });
  const [draft, setDraft] = useState(() => sessionStorage.getItem(`ticket-draft:${id}`) || "");
  const [selectedAgents, setSelectedAgents] = useState<string[]>([]);
  const [selectedDocuments, setSelectedDocuments] = useState<string[]>([]);
  const [file, setFile] = useState<File | null>(null);
  const [queued, setQueued] = useState(false);
  const transcript = useRef<HTMLDivElement>(null);

  useEffect(() => sessionStorage.setItem(`ticket-draft:${id}`, draft), [id, draft]);
  useEffect(() => {
    if (!query.data?.personas.length || selectedAgents.length) return;
    setSelectedAgents([query.data.personas[0].ID]);
  }, [query.data?.personas]);
  useEffect(() => {
    const stream = new EventSource(`/api/v2/work/${encodeURIComponent(id)}/events`);
    stream.addEventListener("snapshot", event => {
      const next = JSON.parse((event as MessageEvent).data) as TicketPayload;
      queryClient.setQueryData(["ticket", id], next);
      queryClient.invalidateQueries({ queryKey: ["work"] });
	  queryClient.invalidateQueries({ queryKey: ["documents"] });
    });
    return () => stream.close();
  }, [id, queryClient]);
  useEffect(() => {
    const node = transcript.current;
    if (!node) return;
    const nearBottom = node.scrollHeight - node.scrollTop - node.clientHeight < 180;
    if (nearBottom) requestAnimationFrame(() => node.scrollTo({ top: node.scrollHeight, behavior: "smooth" }));
  }, [query.data?.messages.length]);

  const send = useMutation({
    mutationFn: (form: FormData) => api.sendTicketMessage(config, id, form),
    onMutate: async form => {
      await queryClient.cancelQueries({ queryKey: ["ticket", id] });
      const previous = queryClient.getQueryData<TicketPayload>(["ticket", id]);
      if (previous) {
        const optimistic: Message = { ID: `optimistic-${Date.now()}`, PersonaName: "", PersonaRole: "", Role: "user", Body: String(form.get("prompt") || ""), Sequence: previous.messages.length + 1, CreatedAt: new Date().toISOString(), Research: [], optimistic: true };
        queryClient.setQueryData(["ticket", id], { ...previous, messages: [...previous.messages, optimistic] });
      }
      return { previous };
    },
    onError: (_error, _form, context) => context?.previous && queryClient.setQueryData(["ticket", id], context.previous),
    onSuccess: next => {
      queryClient.setQueryData(["ticket", id], next);
      setDraft(""); setFile(null); setSelectedDocuments([]); setQueued(false);
      sessionStorage.removeItem(`ticket-draft:${id}`);
    },
  });
  const status = useMutation({ mutationFn: (next: string) => api.updateStatus(config, id, next), onSuccess: next => { queryClient.setQueryData(["ticket", id], next); queryClient.invalidateQueries({ queryKey: ["work"] }); } });

  const data = query.data;
  const active = data ? runActive(data.run.Status) : false;
  useEffect(() => {
    if (!queued || active || !draft.trim() || send.isPending) return;
    submitMessage();
  }, [queued, active]);

  function submitMessage(event?: FormEvent) {
    event?.preventDefault();
    if (!draft.trim() || !selectedAgents.length) return;
    if (active) { setQueued(true); return; }
    const form = new FormData();
    form.set("prompt", draft.trim());
    selectedAgents.forEach(agent => form.append("persona_id", agent));
    selectedDocuments.forEach(document => form.append("document_id", document));
    if (file) form.set("document", file);
    send.mutate(form);
  }

  if (query.isLoading) return <TicketSkeleton/>;
  if (query.isError || !data) return <ErrorState message={query.error?.message || "Ticket unavailable"} retry={() => query.refetch()}/>;
  const item = data.item;
  return <section className="v2-ticket">
    <div className="v2-ticket-nav"><Link to="/work"><ArrowLeft size={17}/> Work</Link><span>/</span><span>#{pad(item.Number)}</span><div className="v2-ticket-live"><i className={active ? "working" : "ready"}/>{active ? "Agents working" : "Up to date"}</div></div>
    <header className="v2-ticket-header">
      <div><div className="v2-ticket-kicker"><KindBadge item={item}/><PriorityBadge priority={item.Priority}/></div><h1>{item.Title}</h1><p>{item.Description}</p></div>
      <div className="v2-status-actions"><StatusBadge status={item.Status}/>{statusActions(item.Status).map(action => <button key={action.status} disabled={status.isPending} className={`v2-button ${action.primary ? "primary" : "secondary"}`} onClick={() => status.mutate(action.status)}>{action.label}</button>)}</div>
    </header>
    <div className="v2-ticket-grid">
      <section className="v2-conversation-panel">
        <div className="v2-panel-heading"><div><span>Agent workspace</span><h2>Conversation</h2></div>{active && <div className="v2-working-indicator"><LoaderCircle size={16}/><span>Thinking and working</span><b/><b/><b/></div>}</div>
        {data.attachments.length > 0 && <div className="v2-attachments"><Paperclip size={15}/>{data.attachments.map(doc => <a key={doc.ID} href={`/documents/${doc.ID}`}>{doc.Name}</a>)}</div>}
        <div className="v2-transcript" ref={transcript}>
          {!data.messages.length && <EmptyState title="Start the ticket conversation" body="Choose an agent and describe the result you want."/>}
          <AnimatePresence initial={false}>{data.messages.map(message => <MessageBubble key={message.ID} message={message}/>)}</AnimatePresence>
          {data.approvals.length > 0 && <div className="v2-inline-approval"><Sparkles size={19}/><div><strong>{data.approvals.length} decision{data.approvals.length === 1 ? "" : "s"} ready for you</strong><p>The agents have paused at a consequential action.</p></div><a href="/your-turn?tab=approvals">Review</a></div>}
        </div>
        <form className="v2-composer" onSubmit={submitMessage}>
          <div className="v2-agent-chips">{data.personas.map(persona => <button type="button" key={persona.ID} className={selectedAgents.includes(persona.ID) ? "is-selected" : ""} onClick={() => setSelectedAgents(current => current.includes(persona.ID) ? current.filter(id => id !== persona.ID) : [...current, persona.ID])}><span>{initials(persona.Name)}</span>{persona.Name}<Check size={13}/></button>)}</div>
          <textarea value={draft} onChange={e => setDraft(e.target.value)} onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submitMessage(); } }} placeholder="Ask for analysis, a decision, or a concrete next step…"/>
          {data.documents.length > 0 && <details className="v2-document-picker"><summary><BookOpen size={15}/> Attach business context {selectedDocuments.length > 0 && <span>{selectedDocuments.length}</span>}</summary><div>{data.documents.map(doc => <label key={doc.ID}><input type="checkbox" checked={selectedDocuments.includes(doc.ID)} onChange={() => setSelectedDocuments(current => current.includes(doc.ID) ? current.filter(id => id !== doc.ID) : [...current, doc.ID])}/><span>{doc.Name}<small>{doc.MediaType || "Document"}</small></span></label>)}</div></details>}
          <footer><label className="v2-file-button"><Paperclip size={17}/><input type="file" onChange={e => setFile(e.target.files?.[0] || null)}/><span>{file ? file.name : "Upload"}</span></label><span className="v2-composer-hint">Enter to send · Shift+Enter for a new line</span><button className="v2-send" disabled={!draft.trim() || !selectedAgents.length || send.isPending} type="submit">{send.isPending ? <LoaderCircle className="spin" size={18}/> : active ? <><Activity size={17}/> Queue</> : <><Send size={17}/> Send</>}</button></footer>
          {queued && <div className="v2-queued"><Check size={15}/> Your message is queued and will send as soon as the current agent run finishes.</div>}
          {send.isError && <div className="v2-error-inline">{send.error.message}</div>}
        </form>
      </section>
      <aside className="v2-ticket-aside">
        <section className="v2-side-card"><div className="v2-side-heading"><span>Progress</span><Activity size={18}/></div><dl><div><dt>Responsibility</dt><dd>{responsibilityLabel(item.Responsibility)}</dd></div><div><dt>Last activity</dt><dd>{relativeTime(item.UpdatedAt)}</dd></div><div><dt>Created by</dt><dd>{sourceLabel(item.Source)}</dd></div></dl></section>
        <section className="v2-side-card"><div className="v2-side-heading"><span>Subtasks</span><em>{data.subtasks.length}</em></div>{data.subtasks.length ? <div className="v2-subtasks">{data.subtasks.map(subtask => <Link key={subtask.ID} to={`/work/${subtask.ID}`}><StatusDot status={subtask.Status}/><span><strong>{subtask.Title}</strong><small>#{pad(subtask.Number)} · {responsibilityLabel(subtask.Responsibility)}</small></span><ChevronRight size={16}/></Link>)}</div> : <p className="v2-muted">Agents can propose and create subtasks as they break down the work.</p>}</section>
      </aside>
    </div>
  </section>;
}

function YourTurn() {
  const [params, setParams] = useSearchParamsV2();
  const queryClient = useQueryClient();
  const tab = (["input", "reviews", "approvals"].includes(params.get("tab") || "") ? params.get("tab") : "input") as YourTurnPayload["tab"];
  const history = params.get("history") === "1";
  const search = `?tab=${tab}${history ? "&history=1" : ""}`;
  const query = useQuery({ queryKey: ["your-turn", tab, history], queryFn: () => api.yourTurn(search), refetchInterval: 5_000, refetchIntervalInBackground: false });
  const data = query.data;

  useEffect(() => {
    const stream = new EventSource(`/api/v2/your-turn/events${search}`);
    stream.addEventListener("snapshot", event => {
      queryClient.setQueryData(["your-turn", tab, history], JSON.parse((event as MessageEvent).data) as YourTurnPayload);
    });
    return () => stream.close();
  }, [tab, history, search, queryClient]);

  const setTab = (nextTab: YourTurnPayload["tab"]) => {
    const next = new URLSearchParams();
    next.set("tab", nextTab);
    if (history) next.set("history", "1");
    setParams(next);
  };

  return <section className="v2-page v2-your-turn">
    <PageHeading eyebrow="Focused owner attention" title="Your turn" description="One calm place for the decisions and private context only you can provide." actions={<button className="v2-button secondary" onClick={() => { const next = new URLSearchParams(params); history ? next.delete("history") : next.set("history", "1"); setParams(next); }}>{history ? "Show open" : "View history"}</button>}/>
    {data && <div className="v2-attention-tabs">
      <AttentionTab icon={MessageSquareText} label="Talk with Mia" detail="Consolidated questions" count={data.counts.Inputs} active={tab === "input"} onClick={() => setTab("input")}/>
      <AttentionTab icon={ClipboardCheck} label="Ready for review" detail="Completed agent work" count={data.counts.Reviews} active={tab === "reviews"} onClick={() => setTab("reviews")}/>
      <AttentionTab icon={Sparkles} label="Approvals" detail="Consequential actions" count={data.counts.Approvals} active={tab === "approvals"} onClick={() => setTab("approvals")}/>
    </div>}
    {query.isLoading && <div className="v2-skeleton conversation"/>}
    {query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {data && tab === "input" && <MiaCoordinator data={data} onUpdate={next => queryClient.setQueryData(["your-turn", tab, history], next)}/>} 
    {data && tab !== "input" && <DecisionQueue items={data.approvals} tab={tab} onUpdate={next => queryClient.setQueryData(["your-turn", tab, history], next)}/>} 
  </section>;
}

function AttentionTab({ icon: Icon, label, detail, count, active, onClick }: { icon: typeof MessageSquareText; label: string; detail: string; count: number; active: boolean; onClick: () => void }) {
  return <button className={active ? "is-active" : ""} onClick={onClick}><span><Icon size={18}/></span><div><strong>{label}</strong><small>{detail}</small></div><em>{count}</em>{active && <motion.i layoutId="attention-active"/>}</button>;
}

function MiaCoordinator({ data, onUpdate }: { data: YourTurnPayload; onUpdate: (next: YourTurnPayload) => void }) {
  const coordinator = data.coordinator;
  const current = coordinator.Current;
  const transcript = useRef<HTMLDivElement>(null);
  const [answer, setAnswer] = useState("");
  const [selectedDocuments, setSelectedDocuments] = useState<string[]>([]);
  const [file, setFile] = useState<File | null>(null);
  const queryClient = useQueryClient();
  useEffect(() => {
    const node = transcript.current;
    if (node) requestAnimationFrame(() => node.scrollTo({ top: node.scrollHeight, behavior: "smooth" }));
  }, [coordinator.Messages?.length]);
  useEffect(() => { setAnswer(""); setSelectedDocuments([]); setFile(null); }, [current?.FactKey]);

  const answerMutation = useMutation({
    mutationFn: (mode?: string) => {
      if (!current) throw new Error("There is no active question.");
      const form = new FormData();
      form.set("fact_key", current.FactKey);
      form.set("answer", answer.trim());
      if (mode) form.set("response_mode", mode);
      selectedDocuments.forEach(id => form.append("document_id", id));
      if (file) form.set("document", file);
      return api.answerCoordinator(config, form);
    },
    onSuccess: async () => {
      const next = await api.yourTurn("?tab=input");
      onUpdate(next);
      queryClient.invalidateQueries({ queryKey: ["work"] });
    },
  });

  return <div className="v2-mia-grid">
    <section className="v2-mia-panel">
      <header className="v2-mia-header"><div className="v2-mia-identity"><span>M</span><div><small>Business analyst</small><h2>Mia</h2><p>Your answers become reusable context for every agent.</p></div></div><div className="v2-mia-stats"><div><strong>{coordinator.PendingRequests}</strong><span>tickets waiting</span></div><div><strong>{coordinator.RemainingTopics}</strong><span>topics left</span></div><div><strong>{coordinator.KnownFacts}</strong><span>facts known</span></div></div></header>
      <div className="v2-mia-transcript" ref={transcript}>
        {(coordinator.Messages || []).map((message, index) => <CoordinatorBubble key={`${message.CreatedAt}-${index}`} message={message}/>) }
        {!coordinator.Messages?.length && <EmptyState title="Mia is reviewing the open work" body="Questions that need owner context will appear here."/>}
      </div>
      {current ? <form className="v2-mia-compose" onSubmit={event => { event.preventDefault(); if (answer.trim()) answerMutation.mutate(undefined); }}>
        <div className="v2-current-question"><span>Mia is asking about {current.Label}</span><p>{current.Prompt}</p>{current.Tickets?.length > 0 && <div><strong>This helps:</strong>{current.Tickets.map(ticket => <Link key={ticket.ID} to={`/work/${ticket.ID}`}>#{pad(ticket.Number)} {ticket.Title}</Link>)}</div>}</div>
        <textarea value={answer} onChange={event => setAnswer(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && answer.trim()) { event.preventDefault(); answerMutation.mutate(undefined); } }} placeholder="Answer naturally. Mia will share the useful facts with the right tickets."/>
        {data.documents.length > 0 && <details className="v2-document-picker"><summary><Paperclip size={15}/> Add supporting records {selectedDocuments.length > 0 && <span>{selectedDocuments.length}</span>}</summary><div>{data.documents.map(document => <label key={document.ID}><input type="checkbox" checked={selectedDocuments.includes(document.ID)} onChange={() => setSelectedDocuments(current => current.includes(document.ID) ? current.filter(id => id !== document.ID) : [...current, document.ID])}/><span>{document.Name}<small>{document.MediaType}</small></span></label>)}</div></details>}
        <footer><div><button type="button" onClick={() => answerMutation.mutate("unknown")}>I don’t know yet</button><button type="button" onClick={() => answerMutation.mutate("not_applicable")}>Not applicable</button></div><label className="v2-file-button"><Paperclip size={17}/><input type="file" onChange={event => setFile(event.target.files?.[0] || null)}/><span>{file ? file.name : "Upload"}</span></label><button className="v2-send" disabled={!answer.trim() || answerMutation.isPending}>{answerMutation.isPending ? <LoaderCircle className="spin" size={18}/> : <><Send size={17}/> Answer and continue</>}</button></footer>
        {answerMutation.isError && <div className="v2-error-inline">{answerMutation.error.message}</div>}
      </form> : <div className="v2-mia-complete"><Check size={22}/><div><strong>You’re caught up</strong><p>Mia has shared the available owner context with the waiting agents.</p></div></div>}
    </section>
    <aside className="v2-mia-aside"><section className="v2-side-card"><div className="v2-side-heading"><span>Shared business memory</span><BookOpen size={18}/></div><p className="v2-muted">Recent facts Mia can reuse without asking you again.</p><div className="v2-fact-list">{(coordinator.RecentFacts || []).slice(0,8).map(fact => <div key={fact.Key}><strong>{fact.Label}</strong><p>{fact.Value}</p><small>{fact.SourceType} · {relativeTime(fact.UpdatedAt)}</small></div>)}</div></section></aside>
  </div>;
}

function CoordinatorBubble({ message }: { message: CoordinatorMessage }) {
  const user = message.Role === "user";
  return <motion.article className={`v2-coordinator-message ${user ? "is-user" : ""}`} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}><div><strong>{user ? "You" : "Mia"}</strong><time>{relativeTime(message.CreatedAt)}</time></div><p>{message.Body}</p></motion.article>;
}

function DecisionQueue({ items, tab, onUpdate }: { items: Approval[]; tab: "reviews" | "approvals"; onUpdate: (next: YourTurnPayload) => void }) {
  return <motion.div className="v2-decision-list" layout>{items.map(item => <DecisionCard key={item.ID} item={item} onUpdate={onUpdate}/>)}{!items.length && <EmptyState title={tab === "reviews" ? "No work is waiting for review" : "No actions need approval"} body="When an agent reaches a consequential decision, it will appear here with the evidence you need."/>}</motion.div>;
}

function DecisionCard({ item, onUpdate }: { item: Approval; onUpdate: (next: YourTurnPayload) => void }) {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (decision: "approve" | "reject") => api.decideApproval(config, item.ID, decision),
    onSuccess: next => {
      onUpdate(next);
      queryClient.invalidateQueries({ queryKey: ["work"] });
      queryClient.invalidateQueries({ queryKey: ["ticket"] });
      queryClient.invalidateQueries({ queryKey: ["conversation"] });
      queryClient.invalidateQueries({ queryKey: ["home"] });
    },
  });
  const review = item.ActionType === "work.review";
  return <motion.article className="v2-decision-card" layout initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, height: 0 }}><header><div className="v2-message-avatar">{initials(item.PersonaName)}</div><div><span>{review ? "Agent work ready" : actionLabel(item.ActionType)}</span><h2>{item.WorkItemTitle || item.Reason || "Review this proposed action"}</h2><p>{item.PersonaName} · {item.PersonaRole}</p></div><StatusBadge status={item.Status}/></header><div className="v2-decision-body">{item.ReviewSummary && <p className="v2-review-summary">{item.ReviewSummary}</p>} {!item.ReviewSummary && <p>{item.Reason}</p>}{item.Recommendations?.length > 0 && <ul>{item.Recommendations.map(recommendation => <li key={recommendation}>{recommendation}</li>)}</ul>}{item.Evidence?.length > 0 && <details><summary>Evidence considered</summary><ul>{item.Evidence.map(evidence => <li key={evidence}>{evidence}</li>)}</ul></details>}</div><footer><button className="v2-button secondary" disabled={mutation.isPending} onClick={() => mutation.mutate("reject")}>{review ? "Needs more work" : "Decline"}</button><button className="v2-button primary" disabled={mutation.isPending} onClick={() => mutation.mutate("approve")}>{mutation.isPending ? <LoaderCircle className="spin" size={17}/> : <Check size={17}/>} {review ? "Accept work" : "Approve"}</button></footer>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}</motion.article>;
}

function Documents() {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: ["documents"], queryFn: api.documents, refetchInterval: 5_000, refetchIntervalInBackground: false });
  const [search, setSearch] = useState("");
  const [dragging, setDragging] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const documents = useMemo(() => {
    const term = search.trim().toLowerCase();
    return (query.data?.documents || []).filter(document => !term || `${document.Name} ${document.MediaType}`.toLowerCase().includes(term));
  }, [query.data, search]);

  const upload = async (file?: File) => {
    if (!file || uploading) return;
    setUploading(true); setUploadError("");
    const form = new FormData(); form.set("document", file); form.set("name", file.name);
    try {
      const result = await api.uploadDocument(config, form);
      queryClient.setQueryData(["documents"], (current: { documents: Document[] } | undefined) => ({ documents: [result.document, ...(current?.documents || [])] }));
      navigate(`/documents/${result.document.ID}?uploaded=1`);
    } catch (error) { setUploadError(error instanceof Error ? error.message : "The document could not be uploaded."); }
    finally { setUploading(false); setDragging(false); }
  };

  return <section className="v2-page v2-documents-page">
    <PageHeading eyebrow="Business memory" title="Documents" description="The trusted records your agents can find, cite, and use across every piece of work." actions={<button className="v2-button primary" onClick={() => input.current?.click()}><UploadCloud size={17}/> Upload document</button>}/>
    <input ref={input} hidden type="file" accept=".txt,.md,.csv,.pdf,.doc,.docx,.html,.htm,.rtf,.xlsx,.xls,.pptx" onChange={event => upload(event.target.files?.[0])}/>
    <div className="v2-document-summary"><div><strong>{query.data?.documents.length || 0}</strong><span>records available to agents</span></div><div><Sparkles size={19}/><span>New uploads are indexed automatically for document recall.</span></div></div>
    <div className="v2-document-toolbar"><label className="v2-search"><Search size={17}/><input value={search} onChange={event => setSearch(event.target.value)} placeholder="Search the library…"/></label></div>
    <button type="button" className={`v2-dropzone ${dragging ? "is-dragging" : ""}`} onClick={() => input.current?.click()} onDragEnter={event => { event.preventDefault(); setDragging(true); }} onDragOver={event => event.preventDefault()} onDragLeave={event => { if (event.currentTarget === event.target) setDragging(false); }} onDrop={event => { event.preventDefault(); upload(event.dataTransfer.files?.[0]); }}>
      {uploading ? <LoaderCircle className="spin" size={25}/> : <UploadCloud size={25}/>}<span><strong>{uploading ? "Indexing your document…" : "Drop a document here"}</strong><small>PDF, Word, spreadsheets, presentations, text, and more · up to 15 MB</small></span>
    </button>
    {uploadError && <div className="v2-error-inline">{uploadError}</div>}
    {query.isLoading && <div className="v2-document-grid">{[1,2,3,4,5,6].map(item => <div key={item} className="v2-skeleton document"/>)}</div>}
    {query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {query.data && <motion.div className="v2-document-grid" layout>{documents.map((document, index) => <DocumentCard key={document.ID} document={document} index={index}/>)}{!documents.length && <EmptyState title={search ? "No documents match" : "Your document library is ready"} body={search ? "Try a broader search." : "Upload the first record and your agents will be able to recall it during their work."}/>}</motion.div>}
  </section>;
}

function DocumentCard({ document, index }: { document: Document; index: number }) {
  return <motion.article className="v2-document-card" layout initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: Math.min(index * .025, .18) }} onClick={() => navigate(`/documents/${document.ID}`)} tabIndex={0} onKeyDown={event => event.key === "Enter" && navigate(`/documents/${document.ID}`)}>
    <div className="v2-document-icon"><FileText size={21}/></div><div className="v2-document-card-body"><div><span>{document.MediaType || "Document"}</span><StatusBadge status={document.Status}/></div><h2>{document.Name}</h2><p>{document.CharacterCount.toLocaleString()} characters · {document.ChunkCount} searchable sections · revision {document.Revision || 1}</p><footer><span><Clock3 size={13}/>{document.UploadedBy} · {relativeTime(document.UpdatedAt || document.CreatedAt)}</span><ChevronRight size={17}/></footer></div>
  </motion.article>;
}

function DocumentDetail({ id }: { id: string }) {
  const query = useQuery({ queryKey: ["document", id], queryFn: () => api.document(id) });
  const document = query.data?.document;
  return <section className="v2-page v2-document-detail">
    <div className="v2-ticket-nav"><Link to="/documents"><ArrowLeft size={14}/> Documents</Link>{document && <><ChevronRight size={13}/><span>{document.Name}</span></>}</div>
    {query.isLoading && <><div className="v2-skeleton heading"/><div className="v2-skeleton conversation"/></>}
    {query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {document && <><header className="v2-document-detail-header"><div className="v2-document-icon large"><FileText size={26}/></div><div><span>{document.MediaType || "Document"}</span><h1>{document.Name}</h1><p>{document.ChunkCount} searchable sections · {document.CharacterCount.toLocaleString()} characters · revision {document.Revision || 1} · updated by {document.UploadedBy} {relativeTime(document.UpdatedAt || document.CreatedAt)}</p></div><StatusBadge status={document.Status}/></header><article className="v2-document-content"><header><div><strong>Indexed content</strong><span>This is the text agents search first and can cite in later work.</span></div><Sparkles size={18}/></header><pre>{document.Content || "No extractable text was found in this document."}</pre></article></>}
  </section>;
}

function BoardroomRoom({ id }: { id: string }) {
  const query = useQuery({ queryKey: ["boardroom", id], queryFn: () => api.boardroom(id), refetchInterval: 5_000, refetchIntervalInBackground: false });
  const [prompt, setPrompt] = useState("");
  const [selectedDocuments, setSelectedDocuments] = useState<string[]>([]);
  const mutation = useMutation({ mutationFn: () => { const form = new FormData(); form.set("prompt", prompt.trim()); selectedDocuments.forEach(documentID => form.append("document_id", documentID)); return api.createConversation(config, id, form); }, onSuccess: data => navigate(`/conversations/${data.conversation.ID}`) });
  const data = query.data;
  return <section className="v2-page v2-boardroom-page">
    <div className="v2-ticket-nav"><a href="/"><ArrowLeft size={14}/> Home</a></div>
    {query.isLoading && <TicketSkeleton/>}{query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {data && <><PageHeading eyebrow="Agent workspace" title={data.boardroom.Name} description={data.boardroom.Description || "Bring a business question to the right group of agents."}/><div className="v2-boardroom-grid"><section className="v2-boardroom-start"><header><div><span>Start a conversation</span><h2>What should the team work through?</h2></div><div className="v2-agent-stack">{data.personas.slice(0,6).map(persona => <span key={persona.ID} title={`${persona.Name}, ${persona.Role}`}>{initials(persona.Name)}</span>)}</div></header><textarea autoFocus value={prompt} onChange={event => setPrompt(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && prompt.trim()) { event.preventDefault(); mutation.mutate(); } }} placeholder="Ask naturally. The team can search your documents and the web, create work, and bring consequential decisions back to you."/><details className="v2-document-picker"><summary><Paperclip size={15}/> Attach business records {selectedDocuments.length > 0 && <span>{selectedDocuments.length}</span>}</summary><div>{data.documents.map(document => <label key={document.ID}><input type="checkbox" checked={selectedDocuments.includes(document.ID)} onChange={() => setSelectedDocuments(current => current.includes(document.ID) ? current.filter(value => value !== document.ID) : [...current, document.ID])}/><span>{document.Name}<small>{document.MediaType}</small></span></label>)}{!data.documents.length && <p className="v2-muted">No records yet. <Link to="/documents">Upload a document</Link></p>}</div></details><footer><span>Enter to send · Shift + Enter for a new line</span><button className="v2-send" disabled={!prompt.trim() || mutation.isPending} onClick={() => mutation.mutate()}>{mutation.isPending ? <LoaderCircle className="spin" size={17}/> : <Send size={17}/>} Bring in the team</button></footer>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}</section><aside className="v2-boardroom-roster"><div className="v2-side-heading"><span>In this room</span><em>{data.personas.length}</em></div>{data.personas.map(persona => <div key={persona.ID}><span>{initials(persona.Name)}</span><p><strong>{persona.Name}</strong><small>{persona.Role}</small></p>{persona.Tools?.includes("web.search") && <Search size={13}/>}</div>)}</aside></div><section className="v2-conversation-history"><header><div><span>Recent conversations</span><h2>Pick up where you left off</h2></div><em>{data.conversations.length}</em></header><div>{data.conversations.map((conversation, index) => <motion.article key={conversation.ID} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: index * .02 }} onClick={() => navigate(`/conversations/${conversation.ID}`)}><StatusDot status={conversation.LatestStatus}/><div><h3>{conversation.Title}</h3><p>{conversation.LatestPrompt}</p><small>{conversation.MessageCount} messages · {relativeTime(conversation.UpdatedAt)}</small></div><ChevronRight size={18}/></motion.article>)}{!data.conversations.length && <EmptyState title="No conversations yet" body="Ask the first question above and the boardroom will come alive."/>}</div></section></>}
  </section>;
}

function BoardroomConversation({ id }: { id: string }) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["conversation", id],
    queryFn: () => api.conversation(id),
    refetchInterval: current => {
      const status = (current.state.data as ConversationPayload | undefined)?.run.Status || "";
      return runActive(status) ? (status === "awaiting_approval" ? 5_000 : 1_500) : false;
    },
    refetchIntervalInBackground: false,
  });
  const [draft, setDraft] = useState(() => sessionStorage.getItem(`conversation-draft:${id}`) || "");
  const [selectedDocuments, setSelectedDocuments] = useState<string[]>([]);
  const initialized = useRef("");
  const transcript = useRef<HTMLDivElement>(null);
  useEffect(() => sessionStorage.setItem(`conversation-draft:${id}`, draft), [draft, id]);
  useEffect(() => {
    const stream = new EventSource(`/api/v2/conversations/${encodeURIComponent(id)}/events`);
    stream.addEventListener("snapshot", event => {
      const next = JSON.parse((event as MessageEvent).data) as ConversationPayload;
      queryClient.setQueryData(["conversation", id], next);
      queryClient.invalidateQueries({ queryKey: ["boardroom", next.boardroom.ID] });
      queryClient.invalidateQueries({ queryKey: ["your-turn"] });
      queryClient.invalidateQueries({ queryKey: ["home"] });
      queryClient.invalidateQueries({ queryKey: ["work"] });
	  queryClient.invalidateQueries({ queryKey: ["documents"] });
    });
    stream.onerror = () => queryClient.invalidateQueries({ queryKey: ["conversation", id], exact: true });
    return () => stream.close();
  }, [id, queryClient]);
  useEffect(() => {
    if (!query.data || initialized.current === id) return;
    initialized.current = id;
    setSelectedDocuments(query.data.documents.filter(document => document.Selected).map(document => document.ID));
  }, [id, query.data]);
  useEffect(() => { const node = transcript.current; if (node) requestAnimationFrame(() => node.scrollTo({ top: node.scrollHeight, behavior: "smooth" })); }, [query.data?.messages.length, query.data?.approvals.length, query.data?.run.Status]);
  const mutation = useMutation({ mutationFn: () => { const form = new FormData(); form.set("prompt", draft.trim()); selectedDocuments.forEach(documentID => form.append("document_id", documentID)); return api.followUp(config, id, form); }, onMutate: () => { const optimistic: Message = { ID: `optimistic-${Date.now()}`, PersonaName: config.user.DisplayName, PersonaRole: "Owner", Role: "user", Body: draft.trim(), Sequence: Date.now(), CreatedAt: new Date().toISOString(), Research: [], optimistic: true }; queryClient.setQueryData(["conversation", id], (current: ConversationPayload | undefined) => current ? { ...current, messages: [...current.messages, optimistic] } : current); }, onSuccess: data => { setDraft(""); sessionStorage.removeItem(`conversation-draft:${id}`); queryClient.setQueryData(["conversation", id], data); queryClient.invalidateQueries({ queryKey: ["boardroom", data.boardroom.ID] }); queryClient.invalidateQueries({ queryKey: ["home"] }); }, onError: () => query.refetch() });
  const data = query.data;
  const active = runActive(data?.run.Status || "");
  return <section className="v2-page v2-boardroom-conversation">
    {query.isLoading && <TicketSkeleton/>}{query.isError && <ErrorState message={query.error.message} retry={() => query.refetch()}/>} 
    {data && <><div className="v2-ticket-nav"><Link to={`/boardrooms/${data.boardroom.ID}`}><ArrowLeft size={14}/> {data.boardroom.Name}</Link><ChevronRight size={13}/><span>Conversation</span><div className="v2-ticket-live"><i className={active ? "working" : ""}/>{active ? "Agents are working" : "Live"}</div></div><header className="v2-ticket-header"><div><div className="v2-ticket-kicker"><span className="v2-kind">Boardroom</span><span className="v2-kind">{data.personas.length} agents</span></div><h1>{data.conversation.Title}</h1><p>{data.conversation.LatestPrompt}</p></div><StatusBadge status={data.run.Status}/></header><div className="v2-ticket-grid"><section className="v2-conversation-panel"><div className="v2-panel-heading"><div><span>Conversation</span><h2>{active ? "The team is working through this" : "Continue with the team"}</h2></div>{active && <div className="v2-working-indicator"><LoaderCircle size={14}/><span>Thinking</span><b/><b/><b/></div>}</div>{data.attachments.length > 0 && <div className="v2-attachments"><Paperclip size={14}/>{data.attachments.map(document => <Link key={document.ID} to={`/documents/${document.ID}`}>{document.Name}</Link>)}</div>}<div className="v2-transcript" ref={transcript}><AnimatePresence initial={false}>{data.messages.map(message => <MessageBubble key={message.ID} message={message}/>)}</AnimatePresence>{data.approvals.map(approval => <div className="v2-inline-approval" key={approval.ID}><ClipboardCheck size={18}/><div><strong>Your decision is needed</strong><p>{approval.Reason}</p></div><Link to="/your-turn?tab=approvals">Review</Link></div>)}{!data.messages.length && active && <div className="v2-agent-thinking"><div className="v2-agent-stack">{data.personas.slice(0,5).map(persona => <span key={persona.ID}>{initials(persona.Name)}</span>)}</div><p>The team has your request and is gathering context.</p></div>}</div><form className="v2-composer" onSubmit={event => { event.preventDefault(); if (draft.trim() && !active) mutation.mutate(); }}><textarea value={draft} onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && draft.trim() && !active) { event.preventDefault(); mutation.mutate(); } }} disabled={active} placeholder={active ? "The team is finishing the current response…" : "Ask a follow-up, add context, or tell the team what to do next."}/><details className="v2-document-picker"><summary><Paperclip size={15}/> Conversation documents {selectedDocuments.length > 0 && <span>{selectedDocuments.length}</span>}</summary><div>{data.documents.map(document => <label key={document.ID}><input type="checkbox" checked={selectedDocuments.includes(document.ID)} onChange={() => setSelectedDocuments(current => current.includes(document.ID) ? current.filter(value => value !== document.ID) : [...current, document.ID])}/><span>{document.Name}<small>{document.MediaType}</small></span></label>)}{!data.documents.length && <p className="v2-muted"><Link to="/documents">Upload a document</Link> to give the team more context.</p>}</div></details><footer><span className="v2-composer-hint">Enter to send · Shift + Enter for a new line</span><button className="v2-send" disabled={!draft.trim() || active || mutation.isPending}>{mutation.isPending ? <LoaderCircle className="spin" size={17}/> : <Send size={17}/>} Send</button></footer>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}</form></section><aside className="v2-ticket-aside"><section className="v2-side-card"><div className="v2-side-heading"><span>Team</span><em>{data.personas.length}</em></div><div className="v2-roster-list">{data.personas.map(persona => <div key={persona.ID}><span>{initials(persona.Name)}</span><p><strong>{persona.Name}</strong><small>{persona.Role}</small></p></div>)}</div></section><section className="v2-side-card"><div className="v2-side-heading"><span>Conversation</span></div><dl><div><dt>Status</dt><dd>{statusLabel(data.run.Status)}</dd></div><div><dt>Messages</dt><dd>{data.messages.length}</dd></div><div><dt>Documents</dt><dd>{data.attachments.length}</dd></div><div><dt>Updated</dt><dd>{relativeTime(data.conversation.UpdatedAt)}</dd></div></dl></section></aside></div></>}
  </section>;
}

function MessageBubble({ message }: { message: Message }) {
  const user = message.Role === "user";
  return <motion.article className={`v2-message ${user ? "is-user" : "is-agent"} ${message.optimistic ? "is-optimistic" : ""}`} initial={{ opacity: 0, y: 10, scale: .985 }} animate={{ opacity: message.optimistic ? .62 : 1, y: 0, scale: 1 }} exit={{ opacity: 0 }} layout>
    <div className="v2-message-avatar">{user ? initials(config.user.DisplayName) : initials(message.PersonaName || "M")}</div>
    <div className="v2-message-body"><header><strong>{user ? "You" : message.PersonaName || "Mainspring"}</strong><span>{user ? "Owner" : message.PersonaRole}</span><time>{relativeTime(message.CreatedAt)}</time></header><p>{message.Body}</p>{message.Research?.length > 0 && <ResearchTrace activities={message.Research}/>}</div>
  </motion.article>;
}

function ResearchTrace({ activities }: { activities: Message["Research"] }) {
  return <details className="v2-research"><summary><Search size={15}/> Research trail <span>{activities.length}</span></summary>{activities.map((activity, index) => <div key={`${activity.Tool}-${index}`}><strong>{activity.Tool === "web.search" ? "Searched the web" : activity.Tool === "documents.search" ? "Searched documents" : "Read a source"}</strong><code>{activity.Query || activity.URL}</code>{activity.Results?.map(result => <a key={result.URL} href={result.URL} target="_blank" rel="noreferrer">{result.Title || result.URL}<small>{result.Excerpt}</small></a>)}</div>)}</details>;
}

function WorkCard({ item, subtasks, index, open }: { item: WorkItem; subtasks: WorkItem[]; index: number; open: () => void }) {
  return <motion.article className="v2-work-card" layout initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: Math.min(index * .025, .2) }} onClick={open} tabIndex={0} onKeyDown={e => e.key === "Enter" && open()}>
    <StatusDot status={item.Status}/><div className="v2-work-card-main"><div><span>#{pad(item.Number)}</span><KindBadge item={item}/><PriorityBadge priority={item.Priority}/></div><h3>{item.Title}</h3><p>{item.Description || "No description yet."}</p><footer><span><Bot size={14}/>{responsibilityLabel(item.Responsibility)}</span><span>{relativeTime(item.UpdatedAt)}</span>{subtasks.length > 0 && <span><ClipboardCheck size={14}/>{subtasks.filter(task => task.Status === "done").length}/{subtasks.length} subtasks</span>}</footer></div><ChevronRight className="v2-card-arrow" size={20}/>
  </motion.article>;
}

function CreateWorkModal({ close, onCreated }: { close: () => void; onCreated: (item: WorkItem) => void }) {
  const mutation = useMutation({ mutationFn: (form: FormData) => api.createWork(config, form), onSuccess: result => onCreated(result.item) });
  return <motion.div className="v2-modal-scrim" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} onMouseDown={e => e.target === e.currentTarget && close()}><motion.form className="v2-modal" initial={{ opacity: 0, y: 24, scale: .98 }} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0, y: 16, scale: .98 }} onSubmit={e => { e.preventDefault(); mutation.mutate(new FormData(e.currentTarget)); }}><header><div><span>New work</span><h2>Create a clear next step</h2></div><button type="button" onClick={close}><X size={19}/></button></header><label>Title<input autoFocus name="title" required maxLength={240} placeholder="What needs to happen?"/></label><label>Description<textarea name="description" maxLength={6000} placeholder="Add the outcome, context, and definition of done…"/></label><div className="v2-form-grid"><label>Type<select name="kind" defaultValue="ticket"><option value="ticket">Ticket</option><option value="todo">To-do</option></select></label><label>Priority<select name="priority" defaultValue="normal"><option value="low">Low</option><option value="normal">Normal</option><option value="high">High</option><option value="urgent">Urgent</option></select></label></div><label className="v2-check"><input type="checkbox" name="assign_to_me" value="true"/><span>Assign this to me</span></label>{mutation.isError && <div className="v2-error-inline">{mutation.error.message}</div>}<footer><button className="v2-button secondary" type="button" onClick={close}>Cancel</button><button className="v2-button primary" disabled={mutation.isPending}>{mutation.isPending ? <LoaderCircle className="spin" size={17}/> : <Plus size={17}/>} Create work</button></footer></motion.form></motion.div>;
}

function PageHeading({ eyebrow, title, description, actions }: { eyebrow: string; title: string; description: string; actions?: React.ReactNode }) { return <header className="v2-page-heading"><div><span>{eyebrow}</span><h1>{title}</h1><p>{description}</p></div>{actions}</header>; }
function Metric({ label, value, tone }: { label: string; value: number; tone: string }) { return <motion.div className={`v2-metric ${tone}`} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}><span>{label}</span><strong>{value}</strong><i/></motion.div>; }
function KindBadge({ item }: { item: WorkItem }) { return <span className="v2-kind">{item.Kind === "ticket" ? "Ticket" : "To-do"}</span>; }
function PriorityBadge({ priority }: { priority: string }) { return priority && priority !== "normal" ? <span className={`v2-priority ${priority}`}>{priority}</span> : null; }
function StatusBadge({ status }: { status: string }) { return <span className={`v2-status ${status}`}><StatusDot status={status}/>{statusLabel(status)}</span>; }
function StatusDot({ status }: { status: string }) { return <i className={`v2-status-dot ${status}`}/>; }
function ErrorState({ message, retry }: { message: string; retry: () => void }) { return <div className="v2-state"><Activity size={28}/><h2>That did not load cleanly</h2><p>{message}</p><button className="v2-button secondary" onClick={retry}>Try again</button></div>; }
function EmptyState({ title, body }: { title: string; body: string }) { return <div className="v2-empty"><Sparkles size={24}/><strong>{title}</strong><p>{body}</p></div>; }
function WorkSkeleton() { return <div className="v2-work-list">{[1,2,3,4].map(i => <div className="v2-skeleton work" key={i}/>)}</div>; }
function TicketSkeleton() { return <div className="v2-ticket"><div className="v2-skeleton heading"/><div className="v2-ticket-grid"><div className="v2-skeleton conversation"/><div className="v2-skeleton aside"/></div></div>; }
function initials(name: string) { return name.split(/\s+/).filter(Boolean).slice(0,2).map(part => part[0]?.toUpperCase()).join("") || "M"; }
function pad(value: number) { return String(value).padStart(4, "0"); }
function runActive(status: string) { return ["pending", "queued", "preparing", "running", "awaiting_approval"].includes(status); }
function statusLabel(value: string) { return ({ active: "Active", in_progress: "In progress", waiting: "Waiting", done: "Done", all: "All work", open: "Open", canceled: "Canceled" } as Record<string,string>)[value] || value; }
function responsibilityLabel(value: string) { return ({ agent: "Agent work", shared: "Shared work", external: "External", owner: "Owner work" } as Record<string,string>)[value] || "Owner work"; }
function sourceLabel(value: string) { return ({ persona: "Agent", schedule: "Schedule", system: "Mainspring", user: "Person" } as Record<string,string>)[value] || "Person"; }
function actionLabel(value: string) { return ({ "email.send": "Email ready to send", "tickets.create": "New work proposed", "work.review": "Agent work ready" } as Record<string,string>)[value] || "Action proposed"; }
function relativeTime(value: string) { const time = new Date(value).getTime(); if (!Number.isFinite(time)) return "Recently"; const seconds = Math.round((time - Date.now()) / 1000); const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }); if (Math.abs(seconds) < 60) return formatter.format(seconds, "second"); const minutes = Math.round(seconds / 60); if (Math.abs(minutes) < 60) return formatter.format(minutes, "minute"); const hours = Math.round(minutes / 60); if (Math.abs(hours) < 24) return formatter.format(hours, "hour"); return formatter.format(Math.round(hours / 24), "day"); }
function statusActions(status: string) { if (status === "open") return [{status:"in_progress",label:"Start",primary:false},{status:"done",label:"Complete",primary:true}]; if (status === "in_progress") return [{status:"waiting",label:"Mark waiting",primary:false},{status:"done",label:"Complete",primary:true}]; if (status === "waiting") return [{status:"in_progress",label:"Resume",primary:false},{status:"done",label:"Complete",primary:true}]; if (status === "done") return [{status:"open",label:"Reopen",primary:false}]; return []; }

createRoot(rootElement).render(<React.StrictMode><QueryClientProvider client={queryClient}><AppShell/></QueryClientProvider></React.StrictMode>);

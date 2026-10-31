import json
out='projects/memory/sdk/contracts.json'
def obj(p,req=()):return dict(type='object',properties=p,required=list(req),additionalProperties=False)
def arr(x,max=100):return dict(type='array',items=x,maxItems=max)
def ref(x):return {'$ref':'#/$defs/'+x}
s={'type':'string'};b={'type':'boolean'};rev=dict(type='string',pattern='^[0-9]+$');uuid=dict(type='string');null={'type':'null'}
D={}
D['Value']={'oneOf':[obj({'type':{'const':t},'value': b if t=='boolean' else s},['type','value']) for t in ['string','boolean','integer','decimal','timestamp','ref']]}
D['Evidence']=obj({'source':s,'confidence':{'type':['number','null'],'minimum':0,'maximum':1},'method':s,'observedAt':{'type':['string','null']}},['source'])
D['EntityRef']={'oneOf':[s,obj({'tempId':s},['tempId'])]}
D['Source']=obj({'origin':{'enum':['user','document','import','inference','system']},'uri':s,'locator':s,'description':s,'observedAt':{'type':['string','null']}},['origin','description'])
D['Definition']=obj({'id':s,'ident':s,'kind':{'enum':['attribute','type']},'description':s,'valueType':{'enum':['string','boolean','integer','decimal','timestamp','ref']},'cardinality':{'enum':['one','many']},'appliesTo':arr(s,20),'referenceTypes':arr(s,20),'unit':s,'status':{'enum':['active','deprecated','']},'replacement':s,'examples':arr(s,20),'notThis':arr(s,20),'aliases':arr(s,20),'schemaRevision':rev,'protected':b},['ident','kind','description'])
ops=[]
common={'entity':ref('EntityRef'),'expectedRevision':rev,'expectedFact':s}
for op in ['createEntity','createSource','assert','replace','retract','addEvidence','addAlias','renameEntity','archiveEntity']:
 p={'op':{'const':op}};req=['op']
 if op.startswith('create'):
  p.update(tempId=s,ident=s,types=arr(s,20));req+=['tempId']
  if op=='createSource':p['source']=ref('Source');req+=['source']
 else:
  p.update(common);req+=['entity']
  if op in ['assert','replace','retract','addEvidence']:p['attribute']=s;req+=['attribute']
  if op in ['assert','replace']:p.update(value=ref('Value'),evidence=arr(ref('Evidence'),20),absent=b,unit=s);req+=['value','evidence']
  if op=='retract':p['value']=ref('Value')
  if op=='addEvidence':p['evidence']=arr(ref('Evidence'),20);req+=['expectedFact','evidence']
  if op in ['renameEntity','addAlias']:p['name']=s;req+=['name']
  if op=='archiveEntity':req+=['expectedRevision']
 ops.append(obj(p,req))
D['Operation']={'oneOf':ops}
D['PathStep']=obj({'attribute':s,'direction':{'enum':['in','out']}},['attribute','direction'])
D['Predicate']=obj({'all':arr(ref('Predicate'),64),'any':arr(ref('Predicate'),64),'attribute':s,'op':{'enum':['exists','eq','lt','lte','gt','gte','in']},'value':ref('Value'),'values':arr(ref('Value'),50),'path':arr(ref('PathStep'),4)})
D['Incoming']=obj({'attribute':s,'select':{'type':'object','additionalProperties':{'oneOf':[{'const':True},ref('Pattern')]}},'limit':{'type':'integer','minimum':1,'maximum':100}},['attribute','select'])
D['Pattern']=obj({'select':{'type':'object','maxProperties':100,'additionalProperties':{'oneOf':[{'const':True},ref('Pattern')]}},'incoming':arr(ref('Incoming'),20),'limit':{'type':'integer','minimum':1,'maximum':100}},['select'])
page={'limit':{'type':'integer','minimum':1,'maximum':100},'cursor':s}
space={'space':dict(type='string',description='A space slug explicitly granted by owner consent.')}
tools={}
def tool(name,p,req,desc,scope):tools['memory_'+name]={'input':dict(obj(dict(space,**p),['space']+req),**{'$defs':D}),'description':desc,'scope':scope}
tool('search',dict(query=s,kind={'enum':['ordinary','attribute','type','source']},types=arr(s,20),**page),[], 'Find candidates by exact names, aliases and lexical text; never chooses an ambiguous concept.','memory:read')
tool('get',dict(entities=dict(arr(s),minItems=1),attributes=arr(s)),['entities'],'Batch exact entity/alias lookup with typed facts, stable IDs and evidence. No fuzzy fallback.','memory:read')
tool('find',dict(where=ref('Predicate'),select=arr(s),types=arr(s,20),query=s,**page),['where'],'Query a bounded typed predicate AST in one read snapshot.','memory:read')
tool('pull',dict(entity=s,pattern=ref('Pattern')),['entity','pattern'],'Bounded nested graph projection with cycle stubs and explicit edge continuation.','memory:read')
tool('history',dict(entity=s,**page),[],'Paginate retained fact/entity/schema events and provenance. Retraction and archival are not erasure.','memory:read')
tool('schema',dict(action={'enum':['search','describe','list','types','help']},entity=s,query=s,**page),['action'],'Discover registered attributes/types or get the read-only Code Mode SDK and examples.','memory:read')
tool('transact',dict(idempotencyKey=dict(type='string',minLength=8,maxLength=200),expectedSchemaRevision=rev,reason=s,preconditions={'type':'object','maxProperties':100,'additionalProperties':rev},operations=dict(arr(ref('Operation')),minItems=1)),['idempotencyKey','expectedSchemaRevision','reason','operations'],'Atomic ordinary mutations with sources and optimistic preconditions. Unknown attributes never register implicitly. Archival retains history.','memory:write')
tool('schema_propose',dict(definition=ref('Definition')),['definition'],'Store an explicit schema proposal with duplicate candidates and registry revision. Does not register.','memory:write')
tool('schema_apply',dict(proposal=s,expectedSchemaRevision=rev,action={'enum':['register','alias','update','deprecate','reject']},target=s,rationale=s),['proposal','expectedSchemaRevision','action','rationale'],'Review and apply a specific schema proposal under the space write lock.','memory:schema:write')
tool('code',dict(source=dict(type='string',maxLength=65536),input={},timeoutMs={'type':'integer','minimum':1,'maximum':5000}),['source','input'],'Run function main(input) in isolated read-only Goja JavaScript. No imports, network, filesystem, async or writes. Exact integers are bigint.','memory:execute')

D['Fact']=obj({'id':s,'entity':s,'attribute':s,'value':ref('Value'),'revision':rev,'virtual':b,'evidence':arr(ref('Evidence'),20)},['id','entity','attribute','value','revision','virtual','evidence'])
D['Entity']=obj({'id':s,'ident':{'type':['string','null']},'kind':{'enum':['ordinary','attribute','type','source']},'revision':rev,'archived':b,'facts':arr(ref('Fact'),10000)},['id','ident','kind','revision','archived','facts'])
D['Hit']=obj({'id':s,'ident':{'type':['string','null']},'kind':s,'snippet':s},['id','ident','kind','snippet'])
D['Event']=obj({'revision':rev,'ordinal':{'type':'integer'},'kind':s,'entity':s,'payload':{},'actor':s,'client':s,'reason':s,'committedAt':s},['revision','ordinal','kind','entity','payload','actor','client','reason','committedAt'])
def page_of(item):return obj({'items':arr(item,10000),'complete':b,'nextCursor':{'type':['string','null']}},['items','complete','nextCursor'])
D['PullResult']=obj({'entity':ref('Entity'),'edges':{'type':'object','additionalProperties':page_of(ref('PullResult'))},'stub':b},['entity','edges','stub'])
D['Commit']=obj({'revision':rev,'schemaRevision':rev,'tempIds':{'type':'object','additionalProperties':s},'applied':{'type':'integer'},'noop':{'type':'integer'},'warnings':arr(s)},['revision','schemaRevision','tempIds','applied','noop','warnings'])
help_result=obj({'sdkVersion':s,'declarations':s,'example':s},['sdkVersion','declarations','example'])
code_result=obj({'value':{},'diagnostics':s,'stats':obj({'graph_operations':{'type':'integer'},'sql_statements':{'type':'integer'},'rows':{'type':'integer'},'edges':{'type':'integer'}})},['value','diagnostics','stats'])
outputs={'memory_search':page_of(ref('Hit')),'memory_get':arr(ref('Entity')),'memory_find':page_of(ref('Entity')),'memory_pull':ref('PullResult'),'memory_history':page_of(ref('Event')),'memory_schema':{'oneOf':[ref('Definition'),page_of(ref('Definition')),help_result]},'memory_transact':ref('Commit'),'memory_schema_apply':ref('Commit'),'memory_schema_propose':obj({'id':s,'revision':rev,'schemaRevision':rev,'candidates':arr(ref('Hit'),20)},['id','revision','schemaRevision','candidates']),'memory_code':code_result}
meta=obj({'requestId':s,'spaceRevision':rev,'schemaRevision':rev,'complete':b,'nextCursor':{'type':['string','null']}},['requestId','spaceRevision','schemaRevision','complete','nextCursor'])
error=obj({'code':s,'message':s,'retryable':b,'details':{}},['code','message','retryable'])
for name,t in tools.items():
 t['output']=obj({'ok':b,'data':outputs[name],'error':error,'meta':meta},['ok','meta'])
 t['output']['$defs']=D
 t['output']['oneOf']=[{'properties':{'ok':{'const':True}},'required':['data']},{'properties':{'ok':{'const':False}},'required':['error']}]
open(out,'w').write(json.dumps(tools,indent=2)+'\n')

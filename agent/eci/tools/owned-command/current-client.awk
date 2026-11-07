# Only a complete Active Camera Clients section establishes ownership.
# Return 0 held, 1 absent, 2 missing/malformed/unknown.
BEGIN { headers=0; ended=0; body=""; held=0 }
/^[[:space:]]*Active Camera Clients:[[:space:]]*$/ { headers++; inside=1; next }
inside && /^[[:space:]]*Allowed user IDs:/ { inside=0; ended++; next }
inside { body=body $0 "\n" }

function trim(value) {
 sub(/^[[:space:]]+/, "", value)
 sub(/[[:space:]]+$/, "", value)
 return value
}

# Conflict device IDs are nonempty scalars; the formatter emits a trailing comma.
function conflicts(value,    entries,n,i) {
 if (value !~ /^\{[^{}()\[\]:]*\}$/) return 0
 value=trim(substr(value,2,length(value)-2))
 if (value=="") return 1
 sub(/,[[:space:]]*$/, "", value)
 value=trim(value)
 if (value=="") return 0
 n=split(value,entries,",")
 for (i=1;i<=n;i++) if (trim(entries[i])=="") return 0
 return 1
}

# One field has an explicit label and a complete value. Duplicate labels fail.
function field(text, upstream,    colon,label,value) {
 text=trim(text)
 colon=index(text,":")
 if (!colon) return 0
 label=trim(substr(text,1,colon-1))
 value=trim(substr(text,colon+1))
 if (label !~ /^[A-Za-z][A-Za-z0-9 _-]*$/ || seen[label]++ || value=="") return 0
 if (label=="Conflicting Client Devices") return conflicts(value)
 if (value ~ /[:{}()\[\]]/) return 0
 if (label=="PID") {
  if (value !~ /^[1-9][0-9]*$/) return 0
  # Prefixes force text comparison even when AWK marks input as numeric.
  if (("p" value)==("p" pid)) recordHeld=1
  return 1
 }
 if (label=="Cost" || label=="Score" || label=="State" || label=="User Id") return value ~ /^[+-]?[0-9]+$/
 if (label=="Camera ID" || label=="Client Package Name") return 1
 return upstream
}

# Parenthesized source records require all core fields. Flat compatibility is single.
function record(text, upstream,    i,c,depth,start,part,label,omittedPackage) {
 for (label in seen) delete seen[label]
 recordHeld=0
 # AOSP prints State immediately adjacent to optional User Id.
 if (match(text,/State:[[:space:]]*[+-]?[0-9]+[[:space:]]*User Id:/)) {
  part=substr(text,RSTART,RLENGTH)
  sub(/User Id:/, ", User Id:", part)
  text=substr(text,1,RSTART-1) part substr(text,RSTART+RLENGTH)
 }
 # The absent package contributes exactly one empty field after User Id.
 if (upstream && match(text,/User Id:[[:space:]]*[+-]?[0-9]+[[:space:]]*,[[:space:]]*,[[:space:]]*Conflicting Client Devices:/)) {
  omittedPackage=1
  part=substr(text,RSTART,RLENGTH)
  sub(/,[[:space:]]*,/, ",", part)
  text=substr(text,1,RSTART-1) part substr(text,RSTART+RLENGTH)
 }
 depth=0; start=1
 for (i=1;i<=length(text);i++) {
  c=substr(text,i,1)
  if (c=="{") depth++
  if (c=="}") depth--
  if (depth<0 || depth>1 || c=="(" || c==")" || c=="[" || c=="]") return 0
  if (c=="," && depth==0) {
   if (!field(substr(text,start,i-start),upstream)) return 0
   start=i+1
  }
 }
 if (depth!=0 || !field(substr(text,start),upstream)) return 0
 if (!seen["Camera ID"] || !seen["PID"]) return 0
 if (omittedPackage && seen["Client Package Name"]) return 0
 if (upstream && (!seen["Cost"] || !seen["Score"] || !seen["State"] || !seen["Conflicting Client Devices"])) return 0
 if (recordHeld) held=1
 return 1
}

END {
 if (pid !~ /^[1-9][0-9]*$/ || headers!=1 || ended!=1 || inside) exit 2
 body=trim(body)
 if (body !~ /^\[/ || body !~ /\]$/) exit 2
 rest=trim(substr(body,2,length(body)-2))
 if (rest=="") exit 1
 if (substr(rest,1,1)!="(") {
  if (!record(rest,0)) exit 2
 } else {
  while (rest!="") {
   if (substr(rest,1,1)!="(") exit 2
   end=index(rest,")")
   if (!end || !record(substr(rest,2,end-2),1)) exit 2
   rest=trim(substr(rest,end+1))
  }
 }
 if (held) exit 0
 exit 1
}

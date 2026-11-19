package com.supportchat.app;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.Uri;
import android.os.Bundle;
import android.view.View;
import android.view.WindowManager;
import android.webkit.*;
import android.widget.*;
import org.json.JSONObject;
import java.net.HttpURLConnection;
import java.net.URL;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public class MainActivity extends Activity {
    private WebView web;
    private String server, directory;
    private final ExecutorService network=Executors.newSingleThreadExecutor();
    private int generation=0;
    private boolean resolving=false, stopped=false;
    private ValueCallback<Uri[]> chooser;
    private PermissionRequest microphone;
    private boolean externalFlow=false, resetOnResume=false;
    private static final int FILE_PICKER=21, AUDIO_PERMISSION=22;

    @Override public void onCreate(Bundle state){
        super.onCreate(null); // Never restore a WebView session or form fields.
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
        getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        server=getPreferences(MODE_PRIVATE).getString("server_url",BuildConfig.SERVER_URL);
        directory=getPreferences(MODE_PRIVATE).getString("bootstrap_url",BuildConfig.BOOTSTRAP_URL);
        if(server.isEmpty()&&directory.isEmpty())showSetup();else checkUpdate();
    }
    private boolean validServer(String value){try{ConnectionResolver.origin(value,BuildConfig.DEBUG);return true;}catch(Exception e){return false;}}
    private void destroyChat(){
        if(chooser!=null){chooser.onReceiveValue(null);chooser=null;}
        if(microphone!=null){microphone.deny();microphone=null;}
        if(web!=null){web.stopLoading();web.loadUrl("about:blank");web.clearCache(true);web.clearHistory();web.clearFormData();web.destroy();web=null;}
    }
    private void showSetup(){
        generation++;resolving=false;destroyChat();
        LinearLayout layout=new LinearLayout(this);layout.setOrientation(LinearLayout.VERTICAL);layout.setPadding(40,100,40,40);layout.setBackgroundColor(Color.rgb(7,13,9));
        TextView title=new TextView(this);title.setText("echat · 连接设置");title.setTextSize(25);layout.addView(title);
        EditText entry=addressInput(layout,"固定配置入口 https://config.example.com",directory);
        EditText direct=addressInput(layout,"业务地址（直连模式）",server);
        TextView error=new TextView(this);layout.addView(error);
        Button connect=new Button(this);connect.setText("保存并检查连接");layout.addView(connect);
        connect.setOnClickListener(v->{
            String nextEntry=entry.getText().toString().trim(), nextServer=direct.getText().toString().trim();
            if((nextEntry.isEmpty()&&nextServer.isEmpty())||(!nextEntry.isEmpty()&&!validServer(nextEntry))||(!nextServer.isEmpty()&&!validServer(nextServer))){error.setText("请填写有效 HTTPS 域名；调试版支持本机地址");return;}
            if(!nextEntry.isEmpty())nextEntry=ConnectionResolver.origin(nextEntry,BuildConfig.DEBUG);
            if(!nextServer.isEmpty())nextServer=ConnectionResolver.origin(nextServer,BuildConfig.DEBUG);
            boolean changed=!nextEntry.equals(directory);
            directory=nextEntry;
            // An unverified typed fallback must never bypass the configured directory's identity check.
            if(changed || directory.isEmpty()){server=directory.isEmpty()?nextServer:"";getPreferences(MODE_PRIVATE).edit().remove("service_id").remove("route_revision").putString("server_url",server).apply();}
            getPreferences(MODE_PRIVATE).edit().putString("bootstrap_url",directory).apply();checkUpdate();
        });
        Button back=new Button(this);back.setText("返回");layout.addView(back);back.setOnClickListener(v->{if(server.isEmpty()&&directory.isEmpty())finish();else checkUpdate();});
        ScrollView scroll=new ScrollView(this);scroll.setFillViewport(true);scroll.addView(layout);setContentView(scroll);
    }
    private EditText addressInput(LinearLayout layout,String hint,String value){
        TextView label=new TextView(this);label.setText(hint);layout.addView(label);
        EditText input=new EditText(this);input.setSingleLine(true);input.setText(value);input.setInputType(android.text.InputType.TYPE_CLASS_TEXT|android.text.InputType.TYPE_TEXT_VARIATION_URI);input.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO);layout.addView(input);return input;
    }
    private JSONObject publicJSON(String address) throws Exception {
        HttpURLConnection connection=(HttpURLConnection)new URL(address).openConnection();
        connection.setInstanceFollowRedirects(false);connection.setConnectTimeout(5000);connection.setReadTimeout(5000);connection.setUseCaches(false);connection.setRequestProperty("Cache-Control","no-cache");
        try{
            if(connection.getResponseCode()!=200)throw new java.io.IOException("入口或业务服务暂不可用");
            try(InputStream in=connection.getInputStream();ByteArrayOutputStream out=new ByteArrayOutputStream()){
                byte[] buf=new byte[4096];int n;while((n=in.read(buf))!=-1){if(out.size()+n>65536)throw new java.io.IOException("响应过大");out.write(buf,0,n);}
                return new JSONObject(out.toString(StandardCharsets.UTF_8.name()));
            }
        }finally{connection.disconnect();}
    }
    private void checkUpdate(){
        final int job=++generation;resolving=true;destroyChat();
        TextView loading=new TextView(this);loading.setText("正在检查连接与更新…");loading.setGravity(android.view.Gravity.CENTER);setContentView(loading);
        final String entry=directory;
        final ConnectionResolver.State previous=new ConnectionResolver.State(server,getPreferences(MODE_PRIVATE).getString("service_id",""),getPreferences(MODE_PRIVATE).getLong("route_revision",0));
        network.execute(()->{
            ConnectionResolver.State result=previous;String failure="";
            try{
                if(!entry.isEmpty())result=ConnectionResolver.resolve(entry,previous,new ConnectionResolver.Fetcher(){
                    public ConnectionResolver.Route route(String origin) throws Exception {JSONObject j=publicJSON(origin+"/api/bootstrap");return new ConnectionResolver.Route(j.getInt("protocol"),j.getString("serviceId"),j.getLong("revision"),j.getString("serverUrl"));}
                    public ConnectionResolver.Identity identity(String origin) throws Exception {JSONObject j=publicJSON(origin+"/api/ready");return new ConnectionResolver.Identity(j.getInt("protocol"),j.getString("serviceId"),"ready".equals(j.getString("status")));}
                },BuildConfig.DEBUG);
                else ConnectionResolver.origin(previous.serverUrl(),BuildConfig.DEBUG);
            }catch(Exception e){failure="更新检查未通过，暂用上次已验证地址";}
            final ConnectionResolver.State resolved=result;final String message=failure;
            runOnUiThread(()->{
                if(job!=generation||isFinishing()||isDestroyed()||stopped)return;
                resolving=false;
                boolean verified=message.isEmpty()||entry.isEmpty()||!previous.serviceId().isEmpty();
                if(!verified||!validServer(resolved.serverUrl())){showConnectionError();return;}
                server=resolved.serverUrl();
                if(message.isEmpty())getPreferences(MODE_PRIVATE).edit().putString("server_url",server).putString("service_id",resolved.serviceId()).putLong("route_revision",resolved.revision()).apply();
                openChat();if(!message.isEmpty())Toast.makeText(this,message,Toast.LENGTH_LONG).show();
            });
        });
    }
    private void showConnectionError(){new AlertDialog.Builder(this).setTitle("连接检查未通过").setMessage("请确认固定配置入口已发布，且新业务服务可访问、身份一致。尚无已验证地址时不会连接未经验证的服务。").setCancelable(false).setPositiveButton("重试",(d,w)->checkUpdate()).setNegativeButton("连接设置",(d,w)->showSetup()).show();}
    private boolean sameOrigin(Uri u){return ConnectionResolver.sameOrigin(server,u.toString(),BuildConfig.DEBUG);}
    private void applicationMenu(){new AlertDialog.Builder(this).setTitle("应用设置 · "+BuildConfig.VERSION_NAME).setItems(new String[]{"检查更新（重新登录）","连接设置"},(d,which)->{if(which==0)checkUpdate();else showSetup();}).setNegativeButton("返回",null).show();}
    private void openChat(){
        if(!validServer(server)){server="";showSetup();return;}
        CookieManager.getInstance().setAcceptCookie(false);CookieManager.getInstance().removeAllCookies(null);WebStorage.getInstance().deleteAllData();
        web=new WebView(this);web.setBackgroundColor(Color.rgb(7,13,9));web.setSaveEnabled(false);web.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS);
        web.setOnApplyWindowInsetsListener((v,insets)->{v.setPadding(insets.getSystemWindowInsetLeft(),insets.getSystemWindowInsetTop(),insets.getSystemWindowInsetRight(),insets.getSystemWindowInsetBottom());return insets.consumeSystemWindowInsets();});
        WebSettings settings=web.getSettings();settings.setUserAgentString(settings.getUserAgentString()+" SupportChatAndroid/"+BuildConfig.VERSION_NAME);settings.setJavaScriptEnabled(true);settings.setDomStorageEnabled(false);settings.setDatabaseEnabled(false);settings.setCacheMode(WebSettings.LOAD_NO_CACHE);settings.setAllowFileAccess(false);settings.setAllowContentAccess(true);settings.setSaveFormData(false);settings.setGeolocationEnabled(false);settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);settings.setMediaPlaybackRequiresUserGesture(true);
        WebView.setWebContentsDebuggingEnabled(false);
        web.clearCache(true);web.clearFormData();web.clearHistory();
        web.setWebViewClient(new WebViewClient(){
            @Override public boolean shouldOverrideUrlLoading(WebView view,WebResourceRequest request){if(request.isForMainFrame()&&"supportchat://refresh".equals(request.getUrl().toString())){view.post(()->checkUpdate());return true;}return !sameOrigin(request.getUrl()) || (request.isForMainFrame() && !BuildConfig.ENTRY_PATH.equals(request.getUrl().getEncodedPath()));}
            @Override public void onReceivedError(WebView view,WebResourceRequest request,WebResourceError error){if(request.isForMainFrame()&&view==web&&!"about:blank".equals(request.getUrl().toString()))runOnUiThread(()->{if(view==web)showError();});}
        });
        web.setDownloadListener((url,agent,disposition,type,length)->Toast.makeText(this,"此应用不支持保存附件到本机",Toast.LENGTH_SHORT).show());
        web.setWebChromeClient(new WebChromeClient(){
            @Override public boolean onShowFileChooser(WebView view,ValueCallback<Uri[]> callback,FileChooserParams params){
                if(chooser!=null)chooser.onReceiveValue(null);chooser=callback;externalFlow=true;
                Intent intent=new Intent(Intent.ACTION_GET_CONTENT);intent.setType("*/*");intent.addCategory(Intent.CATEGORY_OPENABLE);
                try{startActivityForResult(Intent.createChooser(intent,"选择图片或文件"),FILE_PICKER);}catch(Exception e){chooser.onReceiveValue(null);chooser=null;externalFlow=false;}return true;
            }
            @Override public void onPermissionRequest(PermissionRequest request){runOnUiThread(()->{
                if(!sameOrigin(request.getOrigin())){request.deny();return;}
                boolean audio=false;for(String resource:request.getResources())if(PermissionRequest.RESOURCE_AUDIO_CAPTURE.equals(resource))audio=true;
                if(!audio){request.deny();return;}
                if(checkSelfPermission(Manifest.permission.RECORD_AUDIO)==PackageManager.PERMISSION_GRANTED){request.grant(new String[]{PermissionRequest.RESOURCE_AUDIO_CAPTURE});return;}
                if(microphone!=null)microphone.deny();microphone=request;externalFlow=true;requestPermissions(new String[]{Manifest.permission.RECORD_AUDIO},AUDIO_PERMISSION);
            });}
            @Override public void onPermissionRequestCanceled(PermissionRequest request){if(microphone==request)microphone=null;}
        });
        LinearLayout shell=new LinearLayout(this);shell.setOrientation(LinearLayout.VERTICAL);
        shell.setOnApplyWindowInsetsListener((v,insets)->{v.setPadding(insets.getSystemWindowInsetLeft(),insets.getSystemWindowInsetTop(),insets.getSystemWindowInsetRight(),insets.getSystemWindowInsetBottom());return insets.consumeSystemWindowInsets();});
        Button menu=new Button(this);menu.setText("应用设置 · 检查更新");menu.setOnClickListener(v->applicationMenu());shell.addView(menu,new LinearLayout.LayoutParams(-1,(int)(48*getResources().getDisplayMetrics().density)));shell.addView(web,new LinearLayout.LayoutParams(-1,0,1));setContentView(shell);web.loadUrl(server+BuildConfig.ENTRY_PATH);
    }
    private void showError(){if(isFinishing()||stopped)return;new AlertDialog.Builder(this).setTitle("暂时无法连接").setMessage("检查更新可重新获取业务地址。请检查网络后重试。").setPositiveButton("检查更新",(d,w)->checkUpdate()).setNegativeButton("连接设置",(d,w)->showSetup()).show();}
    @Override protected void onActivityResult(int req,int result,Intent data){super.onActivityResult(req,result,data);if(req==FILE_PICKER){externalFlow=false;if(chooser!=null){chooser.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result,data));chooser=null;}}}
    @Override public void onRequestPermissionsResult(int req,String[] permissions,int[] grants){super.onRequestPermissionsResult(req,permissions,grants);if(req==AUDIO_PERMISSION){externalFlow=false;if(microphone!=null){if(grants.length>0&&grants[0]==PackageManager.PERMISSION_GRANTED)microphone.grant(new String[]{PermissionRequest.RESOURCE_AUDIO_CAPTURE});else microphone.deny();microphone=null;}}}
    @Override protected void onPause(){super.onPause();if(web!=null)web.onPause();}
    @Override protected void onStop(){super.onStop();stopped=true;if(!externalFlow&&(web!=null||resolving)){resetOnResume=true;generation++;resolving=false;destroyChat();}}
    @Override protected void onResume(){super.onResume();stopped=false;if(resetOnResume){resetOnResume=false;checkUpdate();}else if(web!=null)web.onResume();}
    @Override public void onBackPressed(){new AlertDialog.Builder(this).setMessage("退出并结束本次会话？").setNegativeButton("取消",null).setPositiveButton("退出",(d,w)->finish()).show();}
    @Override protected void onDestroy(){generation++;network.shutdownNow();destroyChat();super.onDestroy();}
}
